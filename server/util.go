// Copyright 2013 The Go-MySQL-Driver Authors. All rights reserved.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at http://mozilla.org/MPL/2.0/.

// The MIT License (MIT)
//
// Copyright (c) 2014 wandoulabs
// Copyright (c) 2014 siddontang
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

// Copyright 2021 Digital China Group Co.,Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"encoding/binary"
	"github.com/jackc/pgio"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/hack"
)

func parseNullTermString(b []byte) (str []byte, remain []byte) {
	off := bytes.IndexByte(b, 0)
	if off == -1 {
		return nil, b
	}
	return b[:off], b[off+1:]
}

func parseLengthEncodedInt(b []byte) (num uint64, isNull bool, n int) {
	switch b[0] {
	// 251: NULL
	case 0xfb:
		n = 1
		isNull = true
		return

	// 252: value of following 2
	case 0xfc:
		num = uint64(b[1]) | uint64(b[2])<<8
		n = 3
		return

	// 253: value of following 3
	case 0xfd:
		num = uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16
		n = 4
		return

	// 254: value of following 8
	case 0xfe:
		num = uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16 |
			uint64(b[4])<<24 | uint64(b[5])<<32 | uint64(b[6])<<40 |
			uint64(b[7])<<48 | uint64(b[8])<<56
		n = 9
		return
	}

	// https://dev.mysql.com/doc/internals/en/integer.html#length-encoded-integer: If the first byte of a packet is a length-encoded integer and its byte value is 0xfe, you must check the length of the packet to verify that it has enough space for a 8-byte integer.
	// TODO: 0xff is undefined

	// 0-250: value of first byte
	num = uint64(b[0])
	n = 1
	return
}

func dumpLengthEncodedInt(buffer []byte, n uint64) []byte {
	switch {
	case n <= 250:
		return append(buffer, tinyIntCache[n]...)

	case n <= 0xffff:
		return append(buffer, 0xfc, byte(n), byte(n>>8))

	case n <= 0xffffff:
		return append(buffer, 0xfd, byte(n), byte(n>>8), byte(n>>16))

	case n <= 0xffffffffffffffff:
		return append(buffer, 0xfe, byte(n), byte(n>>8), byte(n>>16), byte(n>>24),
			byte(n>>32), byte(n>>40), byte(n>>48), byte(n>>56))
	}

	return buffer
}

func parseLengthEncodedBytes(b []byte) ([]byte, bool, int, error) {
	// Get length
	num, isNull, n := parseLengthEncodedInt(b)
	if num < 1 {
		return nil, isNull, n, nil
	}

	n += int(num)

	// Check data length
	if len(b) >= n {
		return b[n-int(num) : n], false, n, nil
	}

	return nil, false, n, io.EOF
}

func dumpLengthEncodedString(buffer []byte, bytes []byte) []byte {
	buffer = dumpLengthEncodedInt(buffer, uint64(len(bytes)))
	buffer = append(buffer, bytes...)
	return buffer
}

func dumpLengthEncodedStringByBigEndian(buffer []byte, bytes []byte) []byte {
	buffer = pgio.AppendInt32(buffer, int32(len(bytes)))
	buffer = append(buffer, bytes...)
	return buffer
}

func dumpUint16(buffer []byte, n uint16) []byte {
	buffer = append(buffer, byte(n))
	buffer = append(buffer, byte(n>>8))
	return buffer
}

func dumpUint16ByBigEndian(buffer []byte, n uint16) []byte {
	buffer = append(buffer, byte(n>>8))
	buffer = append(buffer, byte(n))
	return buffer
}

func dumpUint32(buffer []byte, n uint32) []byte {
	buffer = append(buffer, byte(n))
	buffer = append(buffer, byte(n>>8))
	buffer = append(buffer, byte(n>>16))
	buffer = append(buffer, byte(n>>24))
	return buffer
}

func dumpUint32ByBigEndian(buffer []byte, n uint32) []byte {
	buffer = append(buffer, byte(n>>24))
	buffer = append(buffer, byte(n>>16))
	buffer = append(buffer, byte(n>>8))
	buffer = append(buffer, byte(n))
	return buffer
}

func dumpUint64(buffer []byte, n uint64) []byte {
	buffer = append(buffer, byte(n))
	buffer = append(buffer, byte(n>>8))
	buffer = append(buffer, byte(n>>16))
	buffer = append(buffer, byte(n>>24))
	buffer = append(buffer, byte(n>>32))
	buffer = append(buffer, byte(n>>40))
	buffer = append(buffer, byte(n>>48))
	buffer = append(buffer, byte(n>>56))
	return buffer
}

func dumpUint64ByBigEndian(buffer []byte, n uint64) []byte {
	buffer = append(buffer, byte(n>>56))
	buffer = append(buffer, byte(n>>48))
	buffer = append(buffer, byte(n>>40))
	buffer = append(buffer, byte(n>>32))
	buffer = append(buffer, byte(n>>24))
	buffer = append(buffer, byte(n>>16))
	buffer = append(buffer, byte(n>>8))
	buffer = append(buffer, byte(n))
	return buffer
}

var tinyIntCache [251][]byte

func init() {
	for i := 0; i < len(tinyIntCache); i++ {
		tinyIntCache[i] = []byte{byte(i)}
	}
}

func dumpBinaryTime(dur time.Duration) (data []byte) {
	if dur == 0 {
		data = tinyIntCache[0]
		return
	}
	data = make([]byte, 13)
	data[0] = 12
	if dur < 0 {
		data[1] = 1
		dur = -dur
	}
	days := dur / (24 * time.Hour)
	dur -= days * 24 * time.Hour
	data[2] = byte(days)
	hours := dur / time.Hour
	dur -= hours * time.Hour
	data[6] = byte(hours)
	minutes := dur / time.Minute
	dur -= minutes * time.Minute
	data[7] = byte(minutes)
	seconds := dur / time.Second
	dur -= seconds * time.Second
	data[8] = byte(seconds)
	if dur == 0 {
		data[0] = 8
		return data[:9]
	}
	binary.LittleEndian.PutUint32(data[9:13], uint32(dur/time.Microsecond))
	return
}

// dumpBinaryTimeByBigEndian 将时间转为大端序字节流
func dumpBinaryTimeByBigEndian(dur time.Duration) (data []byte) {
	if dur == 0 {
		data = tinyIntCache[0]
		return
	}
	data = make([]byte, 13)
	data[0] = 12
	if dur < 0 {
		data[1] = 1
		dur = -dur
	}
	days := dur / (24 * time.Hour)
	dur -= days * 24 * time.Hour
	data[2] = byte(days)
	hours := dur / time.Hour
	dur -= hours * time.Hour
	data[6] = byte(hours)
	minutes := dur / time.Minute
	dur -= minutes * time.Minute
	data[7] = byte(minutes)
	seconds := dur / time.Second
	dur -= seconds * time.Second
	data[8] = byte(seconds)
	if dur == 0 {
		data[0] = 8
		return data[:9]
	}
	binary.BigEndian.PutUint32(data[9:13], uint32(dur/time.Microsecond))
	return
}

func dumpBinaryDateTime(data []byte, t types.Time) []byte {
	year, mon, day := t.Year(), t.Month(), t.Day()
	switch t.Type() {
	case mysql.TypeTimestamp, mysql.TypeDatetime:
		if t.IsZero() {
			// All zero.
			data = append(data, 0)
		} else if t.Microsecond() != 0 {
			// Has micro seconds.
			data = append(data, 11)
			data = dumpUint16(data, uint16(year))
			data = append(data, byte(mon), byte(day), byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
			data = dumpUint32(data, uint32(t.Microsecond()))
		} else if t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 {
			// Has HH:MM:SS
			data = append(data, 7)
			data = dumpUint16(data, uint16(year))
			data = append(data, byte(mon), byte(day), byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
		} else {
			// Only YY:MM:DD
			data = append(data, 4)
			data = dumpUint16(data, uint16(year))
			data = append(data, byte(mon), byte(day))
		}
	case mysql.TypeDate:
		if t.IsZero() {
			data = append(data, 0)
		} else {
			data = append(data, 4)
			data = dumpUint16(data, uint16(year)) //year
			data = append(data, byte(mon), byte(day))
		}
	}
	return data
}

func dumpBinaryDateTimeByBigEndian(data []byte, t types.Time) []byte {
	year, mon, day := t.Year(), t.Month(), t.Day()
	switch t.Type() {
	case mysql.TypeTimestamp, mysql.TypeDatetime:
		if t.IsZero() {
			// All zero.
			data = append(data, 0)
		} else if t.Microsecond() != 0 {
			// Has micro seconds.
			data = append(data, 11)
			data = dumpUint16ByBigEndian(data, uint16(year))
			data = append(data, byte(mon), byte(day), byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
			data = dumpUint32ByBigEndian(data, uint32(t.Microsecond()))
		} else if t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 {
			// Has HH:MM:SS
			data = append(data, 7)
			data = dumpUint16ByBigEndian(data, uint16(year))
			data = append(data, byte(mon), byte(day), byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
		} else {
			// Only YY:MM:DD
			data = append(data, 4)
			data = dumpUint16ByBigEndian(data, uint16(year))
			data = append(data, byte(mon), byte(day))
		}
	case mysql.TypeDate:
		if t.IsZero() {
			data = append(data, 0)
		} else {
			data = append(data, 4)
			data = dumpUint16ByBigEndian(data, uint16(year)) //year
			data = append(data, byte(mon), byte(day))
		}
	}

	return data
}

func dumpBinaryRow(buffer []byte, columns []*ColumnInfo, row chunk.Row) ([]byte, error) {
	buffer = append(buffer, mysql.OKHeader)
	nullBitmapOff := len(buffer)
	numBytes4Null := (len(columns) + 7 + 2) / 8
	for i := 0; i < numBytes4Null; i++ {
		buffer = append(buffer, 0)
	}
	for i := range columns {
		if row.IsNull(i) {
			bytePos := (i + 2) / 8
			bitPos := byte((i + 2) % 8)
			buffer[nullBitmapOff+bytePos] |= 1 << bitPos
			continue
		}
		switch columns[i].Type {
		case mysql.TypeTiny:
			buffer = append(buffer, byte(row.GetInt64(i)))
		case mysql.TypeShort, mysql.TypeYear:
			buffer = dumpUint16(buffer, uint16(row.GetInt64(i)))
		case mysql.TypeInt24, mysql.TypeLong:
			buffer = dumpUint32(buffer, uint32(row.GetInt64(i)))
		case mysql.TypeLonglong:
			buffer = dumpUint64(buffer, row.GetUint64(i))
		case mysql.TypeFloat:
			buffer = dumpUint32(buffer, math.Float32bits(row.GetFloat32(i)))
		case mysql.TypeDouble:
			buffer = dumpUint64(buffer, math.Float64bits(row.GetFloat64(i)))
		case mysql.TypeNewDecimal:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetMyDecimal(i).String()))
		case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
			mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
			buffer = dumpLengthEncodedString(buffer, row.GetBytes(i))
		case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
			buffer = dumpBinaryDateTime(buffer, row.GetTime(i))
		case mysql.TypeDuration:
			buffer = append(buffer, dumpBinaryTime(row.GetDuration(i, 0).Duration)...)
		case mysql.TypeEnum:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetEnum(i).String()))
		case mysql.TypeSet:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetSet(i).String()))
		case mysql.TypeJSON:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetJSON(i).String()))
		default:
			return nil, errInvalidType.GenWithStack("invalid type %v", columns[i].Type)
		}
	}
	return buffer, nil
}

func dumpTextRow(buffer []byte, columns []*ColumnInfo, row chunk.Row) ([]byte, error) {
	tmp := make([]byte, 0, 20)
	for i, col := range columns {
		if row.IsNull(i) {
			buffer = append(buffer, 0xfb)
			continue
		}
		switch col.Type {
		case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong:
			tmp = strconv.AppendInt(tmp[:0], row.GetInt64(i), 10)
			buffer = dumpLengthEncodedString(buffer, tmp)
		case mysql.TypeYear:
			year := row.GetInt64(i)
			tmp = tmp[:0]
			if year == 0 {
				tmp = append(tmp, '0', '0', '0', '0')
			} else {
				tmp = strconv.AppendInt(tmp, year, 10)
			}
			buffer = dumpLengthEncodedString(buffer, tmp)
		case mysql.TypeLonglong:
			if mysql.HasUnsignedFlag(uint(columns[i].Flag)) {
				tmp = strconv.AppendUint(tmp[:0], row.GetUint64(i), 10)
			} else {
				tmp = strconv.AppendInt(tmp[:0], row.GetInt64(i), 10)
			}
			buffer = dumpLengthEncodedString(buffer, tmp)
		case mysql.TypeFloat:
			prec := -1
			if columns[i].Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
				prec = int(col.Decimal)
			}
			tmp = appendFormatFloat(tmp[:0], float64(row.GetFloat32(i)), prec, 32)
			buffer = dumpLengthEncodedString(buffer, tmp)
		case mysql.TypeDouble:
			prec := types.UnspecifiedLength
			if col.Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
				prec = int(col.Decimal)
			}
			tmp = appendFormatFloat(tmp[:0], row.GetFloat64(i), prec, 64)
			buffer = dumpLengthEncodedString(buffer, tmp)
		case mysql.TypeNewDecimal:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetMyDecimal(i).String()))
		case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
			mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
			buffer = dumpLengthEncodedString(buffer, row.GetBytes(i))
		case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetTime(i).String()))
		case mysql.TypeDuration:
			dur := row.GetDuration(i, int(col.Decimal))
			buffer = dumpLengthEncodedString(buffer, hack.Slice(dur.String()))
		case mysql.TypeEnum:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetEnum(i).String()))
		case mysql.TypeSet:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetSet(i).String()))
		case mysql.TypeJSON:
			buffer = dumpLengthEncodedString(buffer, hack.Slice(row.GetJSON(i).String()))
		default:
			return nil, errInvalidType.GenWithStack("invalid type %v", columns[i].Type)
		}
	}
	return buffer, nil
}

// dumpRowData 向客户端写会RowData
// PgSQL 在扩展查询中，会指定每一列返回数据的格式，可能是Text(0)或者Binary(1)
// 当 resultFormat 只有一个值，代表着整行格式都为Text(0)或者Binary(1)
func dumpRowData(data []byte, columns []*ColumnInfo, row chunk.Row, rf []int16) ([]byte, error) {
	if len(rf) == 1 {
		if rf[0] == 1 {
			return dumpBinaryRowData(data, columns, row)
		}
		return dumpTextRowData(data, columns, row)
	}

	return dumpTextOrBinaryRowData(data, columns, row, rf)
}

// dumpBinaryRowData 向客户端以 Binary 格式写回 RowData
// MySQL 报文协议为小端序，在 PgSQL 中报文为大端序
// 每次只写入一行数据
// 这里只写向缓存，并不发送
func dumpBinaryRowData(data []byte, columns []*ColumnInfo, row chunk.Row) ([]byte, error) {
	data = pgio.AppendUint16(data, uint16(len(columns)))

	for i := range columns {
		if row.IsNull(i) {
			data = pgio.AppendInt32(data, -1)
			continue
		}
		switch columns[i].Type {
		// postgresql does not have a tiny type
		//case mysql.TypeTiny:
		//	data = pgio.AppendInt32(data, 1)
		//	data = append(data, byte(row.GetInt64(i)))
		case mysql.TypeTiny, mysql.TypeShort, mysql.TypeYear:
			data = pgio.AppendInt32(data, 2)
			data = dumpUint16ByBigEndian(data, uint16(row.GetInt64(i)))
		case mysql.TypeInt24, mysql.TypeLong:
			data = pgio.AppendInt32(data, 4)
			data = dumpUint32ByBigEndian(data, uint32(row.GetInt64(i)))
		case mysql.TypeLonglong:
			data = pgio.AppendInt32(data, 8)
			data = dumpUint64ByBigEndian(data, row.GetUint64(i))
		case mysql.TypeFloat:
			data = pgio.AppendInt32(data, 4)
			data = dumpUint32ByBigEndian(data, math.Float32bits(row.GetFloat32(i)))
		case mysql.TypeDouble:
			data = pgio.AppendInt32(data, 8)
			data = dumpUint64ByBigEndian(data, math.Float64bits(row.GetFloat64(i)))
		case mysql.TypeNewDecimal:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetMyDecimal(i).String()))
		case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
			mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
			data = dumpLengthEncodedStringByBigEndian(data, row.GetBytes(i))
		case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
			tmp := make([]byte, 0)
			tmp = dumpBinaryDateTimeByBigEndian(tmp, row.GetTime(i))
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeDuration:
			tmp := dumpBinaryTimeByBigEndian(row.GetDuration(i, 0).Duration)
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeEnum:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetEnum(i).String()))
		case mysql.TypeSet:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetSet(i).String()))
		case mysql.TypeJSON:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetJSON(i).String()))
		default:
			return nil, errInvalidType.GenWithStack("invalid type %v", columns[i].Type)
		}
	}

	return data, nil
}

// dumpTextRowData 向客户端以 Text 格式写回 RowData
// 每次只写入一行数据
// 这里只写向缓存，并不发送
func dumpTextRowData(data []byte, columns []*ColumnInfo, row chunk.Row) ([]byte, error) {
	data = pgio.AppendUint16(data, uint16(len(columns)))
	var tmp []byte
	for i, col := range columns {
		if row.IsNull(i) {
			data = pgio.AppendInt32(data, -1)
			continue
		}
		switch col.Type {
		case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong:
			tmp = strconv.AppendInt(nil, row.GetInt64(i), 10)
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeYear:
			year := row.GetInt64(i)
			tmp = nil
			if year == 0 {
				tmp = append(tmp, '0', '0', '0', '0')
			} else {
				tmp = strconv.AppendInt(tmp, year, 10)
			}
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeLonglong:
			if mysql.HasUnsignedFlag(uint(columns[i].Flag)) {
				tmp = strconv.AppendUint(nil, row.GetUint64(i), 10)
			} else {
				tmp = strconv.AppendInt(nil, row.GetInt64(i), 10)
			}
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeFloat:
			prec := -1
			if columns[i].Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
				prec = int(col.Decimal)
			}
			tmp = appendFormatFloat(nil, float64(row.GetFloat32(i)), prec, 32)
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeDouble:
			prec := types.UnspecifiedLength
			if col.Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
				prec = int(col.Decimal)
			}
			tmp = appendFormatFloat(nil, row.GetFloat64(i), prec, 64)
			data = dumpLengthEncodedStringByBigEndian(data, tmp)
		case mysql.TypeNewDecimal:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetMyDecimal(i).String()))
		case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
			mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
			data = dumpLengthEncodedStringByBigEndian(data, row.GetBytes(i))
		case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetTime(i).String()))
		case mysql.TypeDuration:
			dur := row.GetDuration(i, int(col.Decimal))
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(dur.String()))
		case mysql.TypeEnum:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetEnum(i).String()))
		case mysql.TypeSet:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetSet(i).String()))
		case mysql.TypeJSON:
			data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetJSON(i).String()))
		default:
			return nil, errInvalidType.GenWithStack("invalid type %v", columns[i].Type)
		}
	}

	return data, nil
}

// dumpTextOrRowData
func dumpTextOrBinaryRowData(data []byte, columns []*ColumnInfo, row chunk.Row, rf []int16) ([]byte, error) {
	data = pgio.AppendUint16(data, uint16(len(columns)))

	for i, col := range columns {
		if row.IsNull(i) {
			data = pgio.AppendInt32(data, -1)
			continue
		}

		tmp := make([]byte, 0, 20)

		// 只有 text 和 binary 两种
		// binary
		if rf[i] == 1 {
			switch col.Type {
			case mysql.TypeTiny, mysql.TypeShort, mysql.TypeYear:
				data = pgio.AppendInt32(data, 2)
				data = dumpUint16ByBigEndian(data, uint16(row.GetInt64(i)))
			case mysql.TypeInt24, mysql.TypeLong:
				data = pgio.AppendInt32(data, 4)
				data = dumpUint32ByBigEndian(data, uint32(row.GetInt64(i)))
			case mysql.TypeLonglong:
				data = pgio.AppendInt32(data, 8)
				data = dumpUint64ByBigEndian(data, row.GetUint64(i))
			case mysql.TypeFloat:
				data = pgio.AppendInt32(data, 4)
				data = dumpUint32ByBigEndian(data, math.Float32bits(row.GetFloat32(i)))
			case mysql.TypeDouble:
				data = pgio.AppendInt32(data, 8)
				data = dumpUint64ByBigEndian(data, math.Float64bits(row.GetFloat64(i)))
			case mysql.TypeNewDecimal:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetMyDecimal(i).String()))
			case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
				mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
				data = dumpLengthEncodedStringByBigEndian(data, row.GetBytes(i))
			case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
				tmp = dumpBinaryDateTimeByBigEndian(tmp, row.GetTime(i))
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeDuration:
				tmp = dumpBinaryTimeByBigEndian(row.GetDuration(i, 0).Duration)
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeEnum:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetEnum(i).String()))
			case mysql.TypeSet:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetSet(i).String()))
			case mysql.TypeJSON:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetJSON(i).String()))
			default:
				return nil, errInvalidType.GenWithStack("invalid type %v", col.Type)
			}
		} else {
			switch col.Type {
			case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong:
				tmp = strconv.AppendInt(nil, row.GetInt64(i), 10)
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeYear:
				year := row.GetInt64(i)
				tmp = nil
				if year == 0 {
					tmp = append(tmp, '0', '0', '0', '0')
				} else {
					tmp = strconv.AppendInt(tmp, year, 10)
				}
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeLonglong:
				if mysql.HasUnsignedFlag(uint(col.Flag)) {
					tmp = strconv.AppendUint(nil, row.GetUint64(i), 10)
				} else {
					tmp = strconv.AppendInt(nil, row.GetInt64(i), 10)
				}
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeFloat:
				prec := -1
				if col.Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
					prec = int(col.Decimal)
				}
				tmp = appendFormatFloat(nil, float64(row.GetFloat32(i)), prec, 32)
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeDouble:
				prec := types.UnspecifiedLength
				if col.Decimal > 0 && int(col.Decimal) != mysql.NotFixedDec && col.Table == "" {
					prec = int(col.Decimal)
				}
				tmp = appendFormatFloat(nil, row.GetFloat64(i), prec, 64)
				data = dumpLengthEncodedStringByBigEndian(data, tmp)
			case mysql.TypeNewDecimal:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetMyDecimal(i).String()))
			case mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar, mysql.TypeBit,
				mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeBlob:
				data = dumpLengthEncodedStringByBigEndian(data, row.GetBytes(i))
			case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetTime(i).String()))
			case mysql.TypeDuration:
				dur := row.GetDuration(i, int(col.Decimal))
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(dur.String()))
			case mysql.TypeEnum:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetEnum(i).String()))
			case mysql.TypeSet:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetSet(i).String()))
			case mysql.TypeJSON:
				data = dumpLengthEncodedStringByBigEndian(data, hack.Slice(row.GetJSON(i).String()))
			default:
				return nil, errInvalidType.GenWithStack("invalid type %v", col.Type)
			}
		}
	}

	return data, nil
}

func lengthEncodedIntSize(n uint64) int {
	switch {
	case n <= 250:
		return 1

	case n <= 0xffff:
		return 3

	case n <= 0xffffff:
		return 4
	}

	return 9
}

const (
	expFormatBig     = 1e15
	expFormatSmall   = 1e-15
	defaultMySQLPrec = 5
)

func appendFormatFloat(in []byte, fVal float64, prec, bitSize int) []byte {
	absVal := math.Abs(fVal)
	if absVal > math.MaxFloat64 || math.IsNaN(absVal) {
		return []byte{'0'}
	}
	isEFormat := false
	if bitSize == 32 {
		isEFormat = (prec == types.UnspecifiedLength && (float32(absVal) >= expFormatBig || (float32(absVal) != 0 && float32(absVal) < expFormatSmall)))
	} else {
		isEFormat = (prec == types.UnspecifiedLength && (absVal >= expFormatBig || (absVal != 0 && absVal < expFormatSmall)))
	}
	var out []byte

	if isEFormat {
		if bitSize == 32 {
			prec = defaultMySQLPrec
		}
		out = strconv.AppendFloat(in, fVal, 'e', prec, bitSize)
		valStr := out[len(in):]
		// remove the '+' from the string for compatibility.
		plusPos := bytes.IndexByte(valStr, '+')
		if plusPos > 0 {
			plusPosInOut := len(in) + plusPos
			out = append(out[:plusPosInOut], out[plusPosInOut+1:]...)
		}
		// remove extra '0'
		ePos := bytes.IndexByte(valStr, 'e')
		pointPos := bytes.IndexByte(valStr, '.')
		ePosInOut := len(in) + ePos
		pointPosInOut := len(in) + pointPos
		validPos := ePosInOut
		for i := ePosInOut - 1; i >= pointPosInOut; i-- {
			if out[i] == '0' || out[i] == '.' {
				validPos = i
			} else {
				break
			}
		}
		out = append(out[:validPos], out[ePosInOut:]...)
	} else {
		out = strconv.AppendFloat(in, fVal, 'f', prec, bitSize)
	}
	return out
}

// CorsHandler adds Cors Header if `cors` config is set.
type CorsHandler struct {
	handler http.Handler
	cfg     *config.Config
}

func (h CorsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if h.cfg.Cors != "" {
		w.Header().Set("Access-Control-Allow-Origin", h.cfg.Cors)
		w.Header().Set("Access-Control-Allow-Methods", "GET")
	}
	h.handler.ServeHTTP(w, req)
}
