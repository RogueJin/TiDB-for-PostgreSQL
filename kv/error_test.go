// Copyright 2019 PingCAP, Inc.
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

package kv

import (
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/DigitalChinaOpenSource/DCParser/terror"
	. "github.com/pingcap/check"
)

type testErrorSuite struct{}

var _ = Suite(testErrorSuite{})

func (s testErrorSuite) TestError(c *C) {
	kvErrs := []*terror.Error{
		ErrNotExist,
		ErrTxnRetryable,
		ErrCannotSetNilValue,
		ErrInvalidTxn,
		ErrTxnTooLarge,
		ErrEntryTooLarge,
		ErrNotImplemented,
		ErrWriteConflict,
		ErrWriteConflictInTiDB,
	}
	for _, err := range kvErrs {
		code := terror.ToSQLError(err).Code
		c.Assert(code != mysql.ErrUnknown && code == uint16(err.Code()), IsTrue, Commentf("err: %v", err))
	}
}
