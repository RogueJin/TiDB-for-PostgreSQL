// Copyright 2020 PingCAP, Inc.
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

package executor

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DigitalChinaOpenSource/DCParser/charset"
	"github.com/DigitalChinaOpenSource/DCParser/model"
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/DigitalChinaOpenSource/DCParser/terror"
	"github.com/cznic/mathutil"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/domain/infosync"
	"github.com/pingcap/tidb/infoschema"
	"github.com/pingcap/tidb/meta/autoid"
	plannercore "github.com/pingcap/tidb/planner/core"
	"github.com/pingcap/tidb/privilege"
	"github.com/pingcap/tidb/sessionctx"
	"github.com/pingcap/tidb/sessionctx/variable"
	"github.com/pingcap/tidb/statistics"
	"github.com/pingcap/tidb/store/helper"
	"github.com/pingcap/tidb/store/tikv"
	"github.com/pingcap/tidb/table"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/collate"
	"github.com/pingcap/tidb/util/pdapi"
	"github.com/pingcap/tidb/util/set"
	"github.com/pingcap/tidb/util/sqlexec"
	"github.com/pingcap/tidb/util/stmtsummary"
	"go.etcd.io/etcd/clientv3"
)

type memtableRetriever struct {
	dummyCloser
	table       *model.TableInfo
	columns     []*model.ColumnInfo
	rows        [][]types.Datum
	rowIdx      int
	retrieved   bool
	initialized bool
}

// retrieve implements the infoschemaRetriever interface
func (e *memtableRetriever) retrieve(ctx context.Context, sctx sessionctx.Context) ([][]types.Datum, error) {
	if e.retrieved {
		return nil, nil
	}

	//Cache the ret full rows in schemataRetriever
	if !e.initialized {
		is := infoschema.GetInfoSchema(sctx)
		dbs := is.AllSchemas()
		sort.Sort(infoschema.SchemasSorter(dbs))
		var err error
		switch e.table.Name.O {
		case infoschema.TableSchemata:
			e.setDataFromSchemata(sctx, dbs)
		case infoschema.TableStatistics:
			e.setDataForStatistics(sctx, dbs)
		case infoschema.TableTables:
			err = e.setDataFromTables(sctx, dbs)
		case infoschema.TableSequences:
			e.setDataFromSequences(sctx, dbs)
		case infoschema.TablePartitions:
			err = e.setDataFromPartitions(sctx, dbs)
		case infoschema.TableClusterInfo:
			err = e.dataForTiDBClusterInfo(sctx)
		case infoschema.TableAnalyzeStatus:
			e.setDataForAnalyzeStatus(sctx)
		case infoschema.TableTiDBIndexes:
			e.setDataFromIndexes(sctx, dbs)
		case infoschema.TableViews:
			e.setDataFromViews(sctx, dbs)
		case infoschema.TableEngines:
			e.setDataFromEngines()
		case infoschema.TableCharacterSets:
			e.setDataFromCharacterSets()
		case infoschema.TableCollations:
			e.setDataFromCollations()
		case infoschema.TableKeyColumn:
			e.setDataFromKeyColumnUsage(sctx, dbs)
		case infoschema.TableMetricTables:
			e.setDataForMetricTables(sctx)
		case infoschema.TableProfiling:
			e.setDataForPseudoProfiling(sctx)
		case infoschema.TableCollationCharacterSetApplicability:
			e.dataForCollationCharacterSetApplicability()
		case infoschema.TableProcesslist:
			e.setDataForProcessList(sctx)
		case infoschema.ClusterTableProcesslist:
			err = e.setDataForClusterProcessList(sctx)
		case infoschema.TableUserPrivileges:
			e.setDataFromUserPrivileges(sctx)
		case infoschema.TableTiKVRegionPeers:
			err = e.setDataForTikVRegionPeers(sctx)
		case infoschema.TableTiDBHotRegions:
			err = e.setDataForTiDBHotRegions(sctx)
		case infoschema.TableConstraints:
			e.setDataFromTableConstraints(sctx, dbs)
		case infoschema.TableSessionVar:
			err = e.setDataFromSessionVar(sctx)
		case infoschema.TableTiDBServersInfo:
			err = e.setDataForServersInfo()
		case infoschema.TableTiFlashReplica:
			e.dataForTableTiFlashReplica(sctx, dbs)
		case infoschema.TableStatementsSummary,
			infoschema.TableStatementsSummaryHistory,
			infoschema.ClusterTableStatementsSummary,
			infoschema.ClusterTableStatementsSummaryHistory:
			err = e.setDataForStatementsSummary(sctx, e.table.Name.O)
		}
		if err != nil {
			return nil, err
		}
		e.initialized = true
	}

	//Adjust the amount of each return
	maxCount := 1024
	retCount := maxCount
	if e.rowIdx+maxCount > len(e.rows) {
		retCount = len(e.rows) - e.rowIdx
		e.retrieved = true
	}
	ret := make([][]types.Datum, retCount)
	for i := e.rowIdx; i < e.rowIdx+retCount; i++ {
		ret[i-e.rowIdx] = e.rows[i]
	}
	e.rowIdx += retCount
	return adjustColumns(ret, e.columns, e.table), nil
}

func getRowCountAllTable(ctx sessionctx.Context) (map[int64]uint64, error) {
	rows, _, err := ctx.(sqlexec.RestrictedSQLExecutor).ExecRestrictedSQL("select table_id, count from mysql.stats_meta")
	if err != nil {
		return nil, err
	}
	rowCountMap := make(map[int64]uint64, len(rows))
	for _, row := range rows {
		tableID := row.GetInt64(0)
		rowCnt := row.GetUint64(1)
		rowCountMap[tableID] = rowCnt
	}
	return rowCountMap, nil
}

type tableHistID struct {
	tableID int64
	histID  int64
}

func getColLengthAllTables(ctx sessionctx.Context) (map[tableHistID]uint64, error) {
	rows, _, err := ctx.(sqlexec.RestrictedSQLExecutor).ExecRestrictedSQL("select table_id, hist_id, tot_col_size from mysql.stats_histograms where is_index = 0")
	if err != nil {
		return nil, err
	}
	colLengthMap := make(map[tableHistID]uint64, len(rows))
	for _, row := range rows {
		tableID := row.GetInt64(0)
		histID := row.GetInt64(1)
		totalSize := row.GetInt64(2)
		if totalSize < 0 {
			totalSize = 0
		}
		colLengthMap[tableHistID{tableID: tableID, histID: histID}] = uint64(totalSize)
	}
	return colLengthMap, nil
}

func getDataAndIndexLength(info *model.TableInfo, physicalID int64, rowCount uint64, columnLengthMap map[tableHistID]uint64) (uint64, uint64) {
	columnLength := make(map[string]uint64, len(info.Columns))
	for _, col := range info.Columns {
		if col.State != model.StatePublic {
			continue
		}
		length := col.FieldType.StorageLength()
		if length != types.VarStorageLen {
			columnLength[col.Name.L] = rowCount * uint64(length)
		} else {
			length := columnLengthMap[tableHistID{tableID: physicalID, histID: col.ID}]
			columnLength[col.Name.L] = length
		}
	}
	dataLength, indexLength := uint64(0), uint64(0)
	for _, length := range columnLength {
		dataLength += length
	}
	for _, idx := range info.Indices {
		if idx.State != model.StatePublic {
			continue
		}
		for _, col := range idx.Columns {
			if col.Length == types.UnspecifiedLength {
				indexLength += columnLength[col.Name.L]
			} else {
				indexLength += rowCount * uint64(col.Length)
			}
		}
	}
	return dataLength, indexLength
}

type statsCache struct {
	mu         sync.RWMutex
	modifyTime time.Time
	tableRows  map[int64]uint64
	colLength  map[tableHistID]uint64
}

var tableStatsCache = &statsCache{}

// TableStatsCacheExpiry is the expiry time for table stats cache.
var TableStatsCacheExpiry = 3 * time.Second

func (c *statsCache) get(ctx sessionctx.Context) (map[int64]uint64, map[tableHistID]uint64, error) {
	c.mu.RLock()
	if time.Since(c.modifyTime) < TableStatsCacheExpiry {
		tableRows, colLength := c.tableRows, c.colLength
		c.mu.RUnlock()
		return tableRows, colLength, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.modifyTime) < TableStatsCacheExpiry {
		return c.tableRows, c.colLength, nil
	}
	tableRows, err := getRowCountAllTable(ctx)
	if err != nil {
		return nil, nil, err
	}
	colLength, err := getColLengthAllTables(ctx)
	if err != nil {
		return nil, nil, err
	}

	c.tableRows = tableRows
	c.colLength = colLength
	c.modifyTime = time.Now()
	return tableRows, colLength, nil
}

func getAutoIncrementID(ctx sessionctx.Context, schema *model.DBInfo, tblInfo *model.TableInfo) (int64, error) {
	is := infoschema.GetInfoSchema(ctx)
	tbl, err := is.TableByName(schema.Name, tblInfo.Name)
	if err != nil {
		return 0, err
	}
	return tbl.Allocators(ctx).Get(autoid.RowIDAllocType).Base() + 1, nil
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromSchemata(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	rows := make([][]types.Datum, 0, len(schemas))

	for _, schema := range schemas {

		charset := mysql.DefaultCharset
		collation := mysql.DefaultCollationName

		if len(schema.Charset) > 0 {
			charset = schema.Charset // Overwrite default
		}

		if len(schema.Collate) > 0 {
			collation = schema.Collate // Overwrite default
		}

		if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, "", "", mysql.AllPrivMask) {
			continue
		}
		record := types.MakeDatums(
			infoschema.CatalogVal, // CATALOG_NAME
			schema.Name.O,         // SCHEMA_NAME
			charset,               // DEFAULT_CHARACTER_SET_NAME
			collation,             // DEFAULT_COLLATION_NAME
			nil,
		)
		rows = append(rows, record)
	}
	e.rows = rows
}

func (e *memtableRetriever) setDataForStatistics(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			e.setDataForStatisticsInTable(schema, table)
		}
	}
}

func (e *memtableRetriever) setDataForStatisticsInTable(schema *model.DBInfo, table *model.TableInfo) {
	var rows [][]types.Datum
	if table.PKIsHandle {
		for _, col := range table.Columns {
			if mysql.HasPriKeyFlag(col.Flag) {
				record := types.MakeDatums(
					infoschema.CatalogVal, // TABLE_CATALOG
					schema.Name.O,         // TABLE_SCHEMA
					table.Name.O,          // TABLE_NAME
					"0",                   // NON_UNIQUE
					schema.Name.O,         // INDEX_SCHEMA
					"PRIMARY",             // INDEX_NAME
					1,                     // SEQ_IN_INDEX
					col.Name.O,            // COLUMN_NAME
					"A",                   // COLLATION
					0,                     // CARDINALITY
					nil,                   // SUB_PART
					nil,                   // PACKED
					"",                    // NULLABLE
					"BTREE",               // INDEX_TYPE
					"",                    // COMMENT
					"",                    // INDEX_COMMENT
					"YES",                 // IS_VISIBLE
					"NULL",                // Expression
				)
				rows = append(rows, record)
			}
		}
	}
	nameToCol := make(map[string]*model.ColumnInfo, len(table.Columns))
	for _, c := range table.Columns {
		nameToCol[c.Name.L] = c
	}
	for _, index := range table.Indices {
		nonUnique := "1"
		if index.Unique {
			nonUnique = "0"
		}
		for i, key := range index.Columns {
			col := nameToCol[key.Name.L]
			nullable := "YES"
			if mysql.HasNotNullFlag(col.Flag) {
				nullable = ""
			}

			visible := "YES"
			if index.Invisible {
				visible = "NO"
			}

			colName := col.Name.O
			expression := "NULL"
			tblCol := table.Columns[col.Offset]
			if tblCol.Hidden {
				colName = "NULL"
				expression = fmt.Sprintf("(%s)", tblCol.GeneratedExprString)
			}

			record := types.MakeDatums(
				infoschema.CatalogVal, // TABLE_CATALOG
				schema.Name.O,         // TABLE_SCHEMA
				table.Name.O,          // TABLE_NAME
				nonUnique,             // NON_UNIQUE
				schema.Name.O,         // INDEX_SCHEMA
				index.Name.O,          // INDEX_NAME
				i+1,                   // SEQ_IN_INDEX
				colName,               // COLUMN_NAME
				"A",                   // COLLATION
				0,                     // CARDINALITY
				nil,                   // SUB_PART
				nil,                   // PACKED
				nullable,              // NULLABLE
				"BTREE",               // INDEX_TYPE
				"",                    // COMMENT
				"",                    // INDEX_COMMENT
				visible,               // IS_VISIBLE
				expression,            // Expression
			)
			rows = append(rows, record)
		}
	}
	e.rows = append(e.rows, rows...)
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromTables(ctx sessionctx.Context, schemas []*model.DBInfo) error {
	tableRowsMap, colLengthMap, err := tableStatsCache.get(ctx)
	if err != nil {
		return err
	}

	checker := privilege.GetPrivilegeManager(ctx)

	var rows [][]types.Datum
	createTimeTp := mysql.TypeDatetime
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			collation := table.Collate
			if collation == "" {
				collation = mysql.DefaultCollationName
			}
			createTime := types.NewTime(types.FromGoTime(table.GetUpdateTime()), createTimeTp, types.DefaultFsp)

			createOptions := ""

			if table.IsSequence() {
				continue
			}

			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			if !table.IsView() {
				if table.GetPartitionInfo() != nil {
					createOptions = "partitioned"
				}
				var autoIncID interface{}
				hasAutoIncID, _ := infoschema.HasAutoIncrementColumn(table)
				if hasAutoIncID {
					autoIncID, err = getAutoIncrementID(ctx, schema, table)
					if err != nil {
						return err
					}
				}

				var rowCount, dataLength, indexLength uint64
				if table.GetPartitionInfo() == nil {
					rowCount = tableRowsMap[table.ID]
					dataLength, indexLength = getDataAndIndexLength(table, table.ID, rowCount, colLengthMap)
				} else {
					for _, pi := range table.GetPartitionInfo().Definitions {
						rowCount += tableRowsMap[pi.ID]
						parDataLen, parIndexLen := getDataAndIndexLength(table, pi.ID, tableRowsMap[pi.ID], colLengthMap)
						dataLength += parDataLen
						indexLength += parIndexLen
					}
				}
				avgRowLength := uint64(0)
				if rowCount != 0 {
					avgRowLength = dataLength / rowCount
				}
				var tableType string
				switch schema.Name.L {
				case util.InformationSchemaName.L, util.PerformanceSchemaName.L,
					util.MetricSchemaName.L:
					tableType = "SYSTEM VIEW"
				default:
					tableType = "BASE TABLE"
				}
				shardingInfo := infoschema.GetShardingInfo(schema, table)
				record := types.MakeDatums(
					infoschema.CatalogVal, // TABLE_CATALOG
					schema.Name.O,         // TABLE_SCHEMA
					table.Name.O,          // TABLE_NAME
					tableType,             // TABLE_TYPE
					"InnoDB",              // ENGINE
					uint64(10),            // VERSION
					"Compact",             // ROW_FORMAT
					rowCount,              // TABLE_ROWS
					avgRowLength,          // AVG_ROW_LENGTH
					dataLength,            // DATA_LENGTH
					uint64(0),             // MAX_DATA_LENGTH
					indexLength,           // INDEX_LENGTH
					uint64(0),             // DATA_FREE
					autoIncID,             // AUTO_INCREMENT
					createTime,            // CREATE_TIME
					nil,                   // UPDATE_TIME
					nil,                   // CHECK_TIME
					collation,             // TABLE_COLLATION
					nil,                   // CHECKSUM
					createOptions,         // CREATE_OPTIONS
					table.Comment,         // TABLE_COMMENT
					table.ID,              // TIDB_TABLE_ID
					shardingInfo,          // TIDB_ROW_ID_SHARDING_INFO
				)
				rows = append(rows, record)
			} else {
				record := types.MakeDatums(
					infoschema.CatalogVal, // TABLE_CATALOG
					schema.Name.O,         // TABLE_SCHEMA
					table.Name.O,          // TABLE_NAME
					"VIEW",                // TABLE_TYPE
					nil,                   // ENGINE
					nil,                   // VERSION
					nil,                   // ROW_FORMAT
					nil,                   // TABLE_ROWS
					nil,                   // AVG_ROW_LENGTH
					nil,                   // DATA_LENGTH
					nil,                   // MAX_DATA_LENGTH
					nil,                   // INDEX_LENGTH
					nil,                   // DATA_FREE
					nil,                   // AUTO_INCREMENT
					createTime,            // CREATE_TIME
					nil,                   // UPDATE_TIME
					nil,                   // CHECK_TIME
					nil,                   // TABLE_COLLATION
					nil,                   // CHECKSUM
					nil,                   // CREATE_OPTIONS
					"VIEW",                // TABLE_COMMENT
					table.ID,              // TIDB_TABLE_ID
					nil,                   // TIDB_ROW_ID_SHARDING_INFO
				)
				rows = append(rows, record)
			}
		}
	}
	e.rows = rows
	return nil
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *hugeMemTableRetriever) setDataForColumns(ctx sessionctx.Context) error {
	checker := privilege.GetPrivilegeManager(ctx)
	e.rows = e.rows[:0]
	batch := 1024
	for ; e.dbsIdx < len(e.dbs); e.dbsIdx++ {
		schema := e.dbs[e.dbsIdx]
		for e.tblIdx < len(schema.Tables) {
			table := schema.Tables[e.tblIdx]
			e.tblIdx++
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			e.dataForColumnsInTable(schema, table)
			if len(e.rows) >= batch {
				return nil
			}
		}
		e.tblIdx = 0
	}
	return nil
}

func (e *hugeMemTableRetriever) dataForColumnsInTable(schema *model.DBInfo, tbl *model.TableInfo) {
	for i, col := range tbl.Columns {
		if col.Hidden {
			continue
		}
		var charMaxLen, charOctLen, numericPrecision, numericScale, datetimePrecision interface{}
		colLen, decimal := col.Flen, col.Decimal
		defaultFlen, defaultDecimal := mysql.GetDefaultFieldLengthAndDecimal(col.Tp)
		if decimal == types.UnspecifiedLength {
			decimal = defaultDecimal
		}
		if colLen == types.UnspecifiedLength {
			colLen = defaultFlen
		}
		if col.Tp == mysql.TypeSet {
			// Example: In MySQL set('a','bc','def','ghij') has length 13, because
			// len('a')+len('bc')+len('def')+len('ghij')+len(ThreeComma)=13
			// Reference link: https://bugs.mysql.com/bug.php?id=22613
			colLen = 0
			for _, ele := range col.Elems {
				colLen += len(ele)
			}
			if len(col.Elems) != 0 {
				colLen += (len(col.Elems) - 1)
			}
			charMaxLen = colLen
			charOctLen = colLen
		} else if col.Tp == mysql.TypeEnum {
			// Example: In MySQL enum('a', 'ab', 'cdef') has length 4, because
			// the longest string in the enum is 'cdef'
			// Reference link: https://bugs.mysql.com/bug.php?id=22613
			colLen = 0
			for _, ele := range col.Elems {
				if len(ele) > colLen {
					colLen = len(ele)
				}
			}
			charMaxLen = colLen
			charOctLen = colLen
		} else if types.IsString(col.Tp) {
			charMaxLen = colLen
			charOctLen = colLen
		} else if types.IsTypeFractionable(col.Tp) {
			datetimePrecision = decimal
		} else if types.IsTypeNumeric(col.Tp) {
			numericPrecision = colLen
			if col.Tp != mysql.TypeFloat && col.Tp != mysql.TypeDouble {
				numericScale = decimal
			} else if decimal != -1 {
				numericScale = decimal
			}
		}
		columnType := col.FieldType.InfoSchemaStr()
		columnDesc := table.NewColDesc(table.ToColumn(col))
		var columnDefault interface{}
		if columnDesc.DefaultValue != nil {
			columnDefault = fmt.Sprintf("%v", columnDesc.DefaultValue)
		}
		record := types.MakeDatums(
			infoschema.CatalogVal,                // TABLE_CATALOG
			schema.Name.O,                        // TABLE_SCHEMA
			tbl.Name.O,                           // TABLE_NAME
			col.Name.O,                           // COLUMN_NAME
			i+1,                                  // ORIGINAL_POSITION
			columnDefault,                        // COLUMN_DEFAULT
			columnDesc.Null,                      // IS_NULLABLE
			types.TypeToStr(col.Tp, col.Charset), // DATA_TYPE
			charMaxLen,                           // CHARACTER_MAXIMUM_LENGTH
			charOctLen,                           // CHARACTER_OCTET_LENGTH
			numericPrecision,                     // NUMERIC_PRECISION
			numericScale,                         // NUMERIC_SCALE
			datetimePrecision,                    // DATETIME_PRECISION
			columnDesc.Charset,                   // CHARACTER_SET_NAME
			columnDesc.Collation,                 // COLLATION_NAME
			columnType,                           // COLUMN_TYPE
			columnDesc.Key,                       // COLUMN_KEY
			columnDesc.Extra,                     // EXTRA
			"select,insert,update,references",    // PRIVILEGES
			columnDesc.Comment,                   // COLUMN_COMMENT
			col.GeneratedExprString,              // GENERATION_EXPRESSION
		)
		e.rows = append(e.rows, record)
	}
}

func (e *memtableRetriever) setDataFromPartitions(ctx sessionctx.Context, schemas []*model.DBInfo) error {
	tableRowsMap, colLengthMap, err := tableStatsCache.get(ctx)
	if err != nil {
		return err
	}
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	createTimeTp := mysql.TypeDatetime
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.SelectPriv) {
				continue
			}
			createTime := types.NewTime(types.FromGoTime(table.GetUpdateTime()), createTimeTp, types.DefaultFsp)

			var rowCount, dataLength, indexLength uint64
			if table.GetPartitionInfo() == nil {
				rowCount = tableRowsMap[table.ID]
				dataLength, indexLength = getDataAndIndexLength(table, table.ID, rowCount, colLengthMap)
				avgRowLength := uint64(0)
				if rowCount != 0 {
					avgRowLength = dataLength / rowCount
				}
				record := types.MakeDatums(
					infoschema.CatalogVal, // TABLE_CATALOG
					schema.Name.O,         // TABLE_SCHEMA
					table.Name.O,          // TABLE_NAME
					nil,                   // PARTITION_NAME
					nil,                   // SUBPARTITION_NAME
					nil,                   // PARTITION_ORDINAL_POSITION
					nil,                   // SUBPARTITION_ORDINAL_POSITION
					nil,                   // PARTITION_METHOD
					nil,                   // SUBPARTITION_METHOD
					nil,                   // PARTITION_EXPRESSION
					nil,                   // SUBPARTITION_EXPRESSION
					nil,                   // PARTITION_DESCRIPTION
					rowCount,              // TABLE_ROWS
					avgRowLength,          // AVG_ROW_LENGTH
					dataLength,            // DATA_LENGTH
					nil,                   // MAX_DATA_LENGTH
					indexLength,           // INDEX_LENGTH
					nil,                   // DATA_FREE
					createTime,            // CREATE_TIME
					nil,                   // UPDATE_TIME
					nil,                   // CHECK_TIME
					nil,                   // CHECKSUM
					nil,                   // PARTITION_COMMENT
					nil,                   // NODEGROUP
					nil,                   // TABLESPACE_NAME
				)
				rows = append(rows, record)
			} else {
				for i, pi := range table.GetPartitionInfo().Definitions {
					rowCount = tableRowsMap[pi.ID]
					dataLength, indexLength = getDataAndIndexLength(table, pi.ID, tableRowsMap[pi.ID], colLengthMap)

					avgRowLength := uint64(0)
					if rowCount != 0 {
						avgRowLength = dataLength / rowCount
					}

					var partitionDesc string
					if table.Partition.Type == model.PartitionTypeRange {
						partitionDesc = pi.LessThan[0]
					}

					record := types.MakeDatums(
						infoschema.CatalogVal,         // TABLE_CATALOG
						schema.Name.O,                 // TABLE_SCHEMA
						table.Name.O,                  // TABLE_NAME
						pi.Name.O,                     // PARTITION_NAME
						nil,                           // SUBPARTITION_NAME
						i+1,                           // PARTITION_ORDINAL_POSITION
						nil,                           // SUBPARTITION_ORDINAL_POSITION
						table.Partition.Type.String(), // PARTITION_METHOD
						nil,                           // SUBPARTITION_METHOD
						table.Partition.Expr,          // PARTITION_EXPRESSION
						nil,                           // SUBPARTITION_EXPRESSION
						partitionDesc,                 // PARTITION_DESCRIPTION
						rowCount,                      // TABLE_ROWS
						avgRowLength,                  // AVG_ROW_LENGTH
						dataLength,                    // DATA_LENGTH
						uint64(0),                     // MAX_DATA_LENGTH
						indexLength,                   // INDEX_LENGTH
						uint64(0),                     // DATA_FREE
						createTime,                    // CREATE_TIME
						nil,                           // UPDATE_TIME
						nil,                           // CHECK_TIME
						nil,                           // CHECKSUM
						pi.Comment,                    // PARTITION_COMMENT
						nil,                           // NODEGROUP
						nil,                           // TABLESPACE_NAME
					)
					rows = append(rows, record)
				}
			}
		}
	}
	e.rows = rows
	return nil
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromIndexes(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, tb := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, tb.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			if tb.PKIsHandle {
				var pkCol *model.ColumnInfo
				for _, col := range tb.Cols() {
					if mysql.HasPriKeyFlag(col.Flag) {
						pkCol = col
						break
					}
				}
				record := types.MakeDatums(
					schema.Name.O, // TABLE_SCHEMA
					tb.Name.O,     // TABLE_NAME
					0,             // NON_UNIQUE
					"PRIMARY",     // KEY_NAME
					1,             // SEQ_IN_INDEX
					pkCol.Name.O,  // COLUMN_NAME
					nil,           // SUB_PART
					"",            // INDEX_COMMENT
					"NULL",        // Expression
					0,             // INDEX_ID
				)
				rows = append(rows, record)
			}
			for _, idxInfo := range tb.Indices {
				if idxInfo.State != model.StatePublic {
					continue
				}
				for i, col := range idxInfo.Columns {
					nonUniq := 1
					if idxInfo.Unique {
						nonUniq = 0
					}
					var subPart interface{}
					if col.Length != types.UnspecifiedLength {
						subPart = col.Length
					}
					colName := col.Name.O
					expression := "NULL"
					tblCol := tb.Columns[col.Offset]
					if tblCol.Hidden {
						colName = "NULL"
						expression = fmt.Sprintf("(%s)", tblCol.GeneratedExprString)
					}
					record := types.MakeDatums(
						schema.Name.O,   // TABLE_SCHEMA
						tb.Name.O,       // TABLE_NAME
						nonUniq,         // NON_UNIQUE
						idxInfo.Name.O,  // KEY_NAME
						i+1,             // SEQ_IN_INDEX
						colName,         // COLUMN_NAME
						subPart,         // SUB_PART
						idxInfo.Comment, // INDEX_COMMENT
						expression,      // Expression
						idxInfo.ID,      // INDEX_ID
					)
					rows = append(rows, record)
				}
			}
		}
	}
	e.rows = rows
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromViews(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if !table.IsView() {
				continue
			}
			collation := table.Collate
			charset := table.Charset
			if collation == "" {
				collation = mysql.DefaultCollationName
			}
			if charset == "" {
				charset = mysql.DefaultCharset
			}
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			record := types.MakeDatums(
				infoschema.CatalogVal,           // TABLE_CATALOG
				schema.Name.O,                   // TABLE_SCHEMA
				table.Name.O,                    // TABLE_NAME
				table.View.SelectStmt,           // VIEW_DEFINITION
				table.View.CheckOption.String(), // CHECK_OPTION
				"NO",                            // IS_UPDATABLE
				table.View.Definer.String(),     // DEFINER
				table.View.Security.String(),    // SECURITY_TYPE
				charset,                         // CHARACTER_SET_CLIENT
				collation,                       // COLLATION_CONNECTION
			)
			rows = append(rows, record)
		}
	}
	e.rows = rows
}

// DDLJobsReaderExec executes DDLJobs information retrieving.
type DDLJobsReaderExec struct {
	baseExecutor
	DDLJobRetriever

	cacheJobs []*model.Job
	is        infoschema.InfoSchema
}

// Open implements the Executor Next interface.
func (e *DDLJobsReaderExec) Open(ctx context.Context) error {
	if err := e.baseExecutor.Open(ctx); err != nil {
		return err
	}
	txn, err := e.ctx.Txn(true)
	if err != nil {
		return err
	}
	e.DDLJobRetriever.is = e.is
	e.activeRoles = e.ctx.GetSessionVars().ActiveRoles
	err = e.DDLJobRetriever.initial(txn)
	if err != nil {
		return err
	}
	return nil
}

// Next implements the Executor Next interface.
func (e *DDLJobsReaderExec) Next(ctx context.Context, req *chunk.Chunk) error {
	req.GrowAndReset(e.maxChunkSize)
	checker := privilege.GetPrivilegeManager(e.ctx)
	count := 0

	// Append running DDL jobs.
	if e.cursor < len(e.runningJobs) {
		num := mathutil.Min(req.Capacity(), len(e.runningJobs)-e.cursor)
		for i := e.cursor; i < e.cursor+num; i++ {
			e.appendJobToChunk(req, e.runningJobs[i], checker)
			req.AppendString(11, e.runningJobs[i].Query)
		}
		e.cursor += num
		count += num
	}
	var err error

	// Append history DDL jobs.
	if count < req.Capacity() {
		e.cacheJobs, err = e.historyJobIter.GetLastJobs(req.Capacity()-count, e.cacheJobs)
		if err != nil {
			return err
		}
		for _, job := range e.cacheJobs {
			e.appendJobToChunk(req, job, checker)
			req.AppendString(11, job.Query)
		}
		e.cursor += len(e.cacheJobs)
	}
	return nil
}

func (e *memtableRetriever) setDataFromEngines() {
	var rows [][]types.Datum
	rows = append(rows,
		types.MakeDatums(
			"InnoDB",  // Engine
			"DEFAULT", // Support
			"Supports transactions, row-level locking, and foreign keys", // Comment
			"YES", // Transactions
			"YES", // XA
			"YES", // Savepoints
		),
	)
	e.rows = rows
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromCharacterSets() {
	var rows [][]types.Datum
	charsets := charset.GetSupportedCharsets()
	for _, charset := range charsets {
		rows = append(rows,
			types.MakeDatums(charset.Name, charset.DefaultCollation, charset.Desc, charset.Maxlen),
		)
	}
	e.rows = rows
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromCollations() {
	var rows [][]types.Datum
	collations := collate.GetSupportedCollations()
	for _, collation := range collations {
		isDefault := ""
		if collation.IsDefault {
			isDefault = "Yes"
		}
		rows = append(rows,
			types.MakeDatums(collation.Name, collation.CharsetName, collation.ID, isDefault, "Yes", 1),
		)
	}
	e.rows = rows
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) dataForCollationCharacterSetApplicability() {
	var rows [][]types.Datum
	collations := collate.GetSupportedCollations()
	for _, collation := range collations {
		rows = append(rows,
			types.MakeDatums(collation.Name, collation.CharsetName),
		)
	}
	e.rows = rows
}

func (e *memtableRetriever) dataForTiDBClusterInfo(ctx sessionctx.Context) error {
	servers, err := infoschema.GetClusterServerInfo(ctx)
	if err != nil {
		e.rows = nil
		return err
	}
	rows := make([][]types.Datum, 0, len(servers))
	for _, server := range servers {
		startTimeStr := ""
		upTimeStr := ""
		if server.StartTimestamp > 0 {
			startTime := time.Unix(server.StartTimestamp, 0)
			startTimeStr = startTime.Format(time.RFC3339)
			upTimeStr = time.Since(startTime).String()
		}
		row := types.MakeDatums(
			server.ServerType,
			server.Address,
			server.StatusAddr,
			server.Version,
			server.GitHash,
			startTimeStr,
			upTimeStr,
		)
		rows = append(rows, row)
	}
	e.rows = rows
	return nil
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromKeyColumnUsage(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	rows := make([][]types.Datum, 0, len(schemas)) // The capacity is not accurate, but it is not a big problem.
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			rs := keyColumnUsageInTable(schema, table)
			rows = append(rows, rs...)
		}
	}
	e.rows = rows
}

func (e *memtableRetriever) setDataForClusterProcessList(ctx sessionctx.Context) error {
	e.setDataForProcessList(ctx)
	rows, err := infoschema.AppendHostInfoToRows(e.rows)
	if err != nil {
		return err
	}
	e.rows = rows
	return nil
}

func (e *memtableRetriever) setDataForProcessList(ctx sessionctx.Context) {
	sm := ctx.GetSessionManager()
	if sm == nil {
		return
	}

	loginUser := ctx.GetSessionVars().User
	var hasProcessPriv bool
	if pm := privilege.GetPrivilegeManager(ctx); pm != nil {
		if pm.RequestVerification(ctx.GetSessionVars().ActiveRoles, "", "", "", mysql.ProcessPriv) {
			hasProcessPriv = true
		}
	}

	pl := sm.ShowProcessList()

	records := make([][]types.Datum, 0, len(pl))
	for _, pi := range pl {
		// If you have the PROCESS privilege, you can see all threads.
		// Otherwise, you can see only your own threads.
		if !hasProcessPriv && loginUser != nil && pi.User != loginUser.Username {
			continue
		}

		rows := pi.ToRow(ctx.GetSessionVars().StmtCtx.TimeZone)
		record := types.MakeDatums(rows...)
		records = append(records, record)
	}
	e.rows = records
}

func (e *memtableRetriever) setDataFromUserPrivileges(ctx sessionctx.Context) {
	pm := privilege.GetPrivilegeManager(ctx)
	e.rows = pm.UserPrivilegesTable()
}

func (e *memtableRetriever) setDataForMetricTables(ctx sessionctx.Context) {
	var rows [][]types.Datum
	tables := make([]string, 0, len(infoschema.MetricTableMap))
	for name := range infoschema.MetricTableMap {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	for _, name := range tables {
		schema := infoschema.MetricTableMap[name]
		record := types.MakeDatums(
			name,                             // METRICS_NAME
			schema.PromQL,                    // PROMQL
			strings.Join(schema.Labels, ","), // LABELS
			schema.Quantile,                  // QUANTILE
			schema.Comment,                   // COMMENT
		)
		rows = append(rows, record)
	}
	e.rows = rows
}

func keyColumnUsageInTable(schema *model.DBInfo, table *model.TableInfo) [][]types.Datum {
	var rows [][]types.Datum
	if table.PKIsHandle {
		for _, col := range table.Columns {
			if mysql.HasPriKeyFlag(col.Flag) {
				record := types.MakeDatums(
					infoschema.CatalogVal,        // CONSTRAINT_CATALOG
					schema.Name.O,                // CONSTRAINT_SCHEMA
					infoschema.PrimaryConstraint, // CONSTRAINT_NAME
					infoschema.CatalogVal,        // TABLE_CATALOG
					schema.Name.O,                // TABLE_SCHEMA
					table.Name.O,                 // TABLE_NAME
					col.Name.O,                   // COLUMN_NAME
					1,                            // ORDINAL_POSITION
					1,                            // POSITION_IN_UNIQUE_CONSTRAINT
					nil,                          // REFERENCED_TABLE_SCHEMA
					nil,                          // REFERENCED_TABLE_NAME
					nil,                          // REFERENCED_COLUMN_NAME
				)
				rows = append(rows, record)
				break
			}
		}
	}
	nameToCol := make(map[string]*model.ColumnInfo, len(table.Columns))
	for _, c := range table.Columns {
		nameToCol[c.Name.L] = c
	}
	for _, index := range table.Indices {
		var idxName string
		if index.Primary {
			idxName = infoschema.PrimaryConstraint
		} else if index.Unique {
			idxName = index.Name.O
		} else {
			// Only handle unique/primary key
			continue
		}
		for i, key := range index.Columns {
			col := nameToCol[key.Name.L]
			record := types.MakeDatums(
				infoschema.CatalogVal, // CONSTRAINT_CATALOG
				schema.Name.O,         // CONSTRAINT_SCHEMA
				idxName,               // CONSTRAINT_NAME
				infoschema.CatalogVal, // TABLE_CATALOG
				schema.Name.O,         // TABLE_SCHEMA
				table.Name.O,          // TABLE_NAME
				col.Name.O,            // COLUMN_NAME
				i+1,                   // ORDINAL_POSITION,
				nil,                   // POSITION_IN_UNIQUE_CONSTRAINT
				nil,                   // REFERENCED_TABLE_SCHEMA
				nil,                   // REFERENCED_TABLE_NAME
				nil,                   // REFERENCED_COLUMN_NAME
			)
			rows = append(rows, record)
		}
	}
	for _, fk := range table.ForeignKeys {
		fkRefCol := ""
		if len(fk.RefCols) > 0 {
			fkRefCol = fk.RefCols[0].O
		}
		for i, key := range fk.Cols {
			col := nameToCol[key.L]
			record := types.MakeDatums(
				infoschema.CatalogVal, // CONSTRAINT_CATALOG
				schema.Name.O,         // CONSTRAINT_SCHEMA
				fk.Name.O,             // CONSTRAINT_NAME
				infoschema.CatalogVal, // TABLE_CATALOG
				schema.Name.O,         // TABLE_SCHEMA
				table.Name.O,          // TABLE_NAME
				col.Name.O,            // COLUMN_NAME
				i+1,                   // ORDINAL_POSITION,
				1,                     // POSITION_IN_UNIQUE_CONSTRAINT
				schema.Name.O,         // REFERENCED_TABLE_SCHEMA
				fk.RefTable.O,         // REFERENCED_TABLE_NAME
				fkRefCol,              // REFERENCED_COLUMN_NAME
			)
			rows = append(rows, record)
		}
	}
	return rows
}

func (e *memtableRetriever) setDataForTikVRegionPeers(ctx sessionctx.Context) error {
	tikvStore, ok := ctx.GetStore().(tikv.Storage)
	if !ok {
		return errors.New("Information about TiKV region status can be gotten only when the storage is TiKV")
	}
	tikvHelper := &helper.Helper{
		Store:       tikvStore,
		RegionCache: tikvStore.GetRegionCache(),
	}
	regionsInfo, err := tikvHelper.GetRegionsInfo()
	if err != nil {
		return err
	}
	for _, region := range regionsInfo.Regions {
		e.setNewTiKVRegionPeersCols(&region)
	}
	return nil
}

func (e *memtableRetriever) setNewTiKVRegionPeersCols(region *helper.RegionInfo) {
	records := make([][]types.Datum, 0, len(region.Peers))
	pendingPeerIDSet := set.NewInt64Set()
	for _, peer := range region.PendingPeers {
		pendingPeerIDSet.Insert(peer.ID)
	}
	downPeerMap := make(map[int64]int64, len(region.DownPeers))
	for _, peerStat := range region.DownPeers {
		downPeerMap[peerStat.ID] = peerStat.DownSec
	}
	for _, peer := range region.Peers {
		row := make([]types.Datum, len(infoschema.TableTiKVRegionPeersCols))
		row[0].SetInt64(region.ID)
		row[1].SetInt64(peer.ID)
		row[2].SetInt64(peer.StoreID)
		if peer.IsLearner {
			row[3].SetInt64(1)
		} else {
			row[3].SetInt64(0)
		}
		if peer.ID == region.Leader.ID {
			row[4].SetInt64(1)
		} else {
			row[4].SetInt64(0)
		}
		if pendingPeerIDSet.Exist(peer.ID) {
			row[5].SetString(pendingPeer, mysql.DefaultCollationName)
		} else if downSec, ok := downPeerMap[peer.ID]; ok {
			row[5].SetString(downPeer, mysql.DefaultCollationName)
			row[6].SetInt64(downSec)
		} else {
			row[5].SetString(normalPeer, mysql.DefaultCollationName)
		}
		records = append(records, row)
	}
	e.rows = append(e.rows, records...)
}

const (
	normalPeer  = "NORMAL"
	pendingPeer = "PENDING"
	downPeer    = "DOWN"
)

func (e *memtableRetriever) setDataForTiDBHotRegions(ctx sessionctx.Context) error {
	tikvStore, ok := ctx.GetStore().(tikv.Storage)
	if !ok {
		return errors.New("Information about hot region can be gotten only when the storage is TiKV")
	}
	allSchemas := ctx.GetSessionVars().TxnCtx.InfoSchema.(infoschema.InfoSchema).AllSchemas()
	tikvHelper := &helper.Helper{
		Store:       tikvStore,
		RegionCache: tikvStore.GetRegionCache(),
	}
	metrics, err := tikvHelper.ScrapeHotInfo(pdapi.HotRead, allSchemas)
	if err != nil {
		return err
	}
	e.setDataForHotRegionByMetrics(metrics, "read")
	metrics, err = tikvHelper.ScrapeHotInfo(pdapi.HotWrite, allSchemas)
	if err != nil {
		return err
	}
	e.setDataForHotRegionByMetrics(metrics, "write")
	return nil
}

func (e *memtableRetriever) setDataForHotRegionByMetrics(metrics []helper.HotTableIndex, tp string) {
	rows := make([][]types.Datum, 0, len(metrics))
	for _, tblIndex := range metrics {
		row := make([]types.Datum, len(infoschema.TableTiDBHotRegionsCols))
		if tblIndex.IndexName != "" {
			row[1].SetInt64(tblIndex.IndexID)
			row[4].SetString(tblIndex.IndexName, mysql.DefaultCollationName)
		} else {
			row[1].SetNull()
			row[4].SetNull()
		}
		row[0].SetInt64(tblIndex.TableID)
		row[2].SetString(tblIndex.DbName, mysql.DefaultCollationName)
		row[3].SetString(tblIndex.TableName, mysql.DefaultCollationName)
		row[5].SetUint64(tblIndex.RegionID)
		row[6].SetString(tp, mysql.DefaultCollationName)
		if tblIndex.RegionMetric == nil {
			row[7].SetNull()
			row[8].SetNull()
		} else {
			row[7].SetInt64(int64(tblIndex.RegionMetric.MaxHotDegree))
			row[8].SetInt64(int64(tblIndex.RegionMetric.Count))
		}
		row[9].SetUint64(tblIndex.RegionMetric.FlowBytes)
		rows = append(rows, row)
	}
	e.rows = append(e.rows, rows...)
}

// setDataFromTableConstraints constructs data for table information_schema.constraints.See https://dev.mysql.com/doc/refman/5.7/en/table-constraints-table.html
// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromTableConstraints(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, tbl := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, tbl.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			if tbl.PKIsHandle {
				record := types.MakeDatums(
					infoschema.CatalogVal,     // CONSTRAINT_CATALOG
					schema.Name.O,             // CONSTRAINT_SCHEMA
					mysql.PrimaryKeyName,      // CONSTRAINT_NAME
					schema.Name.O,             // TABLE_SCHEMA
					tbl.Name.O,                // TABLE_NAME
					infoschema.PrimaryKeyType, // CONSTRAINT_TYPE
				)
				rows = append(rows, record)
			}

			for _, idx := range tbl.Indices {
				var cname, ctype string
				if idx.Primary {
					cname = mysql.PrimaryKeyName
					ctype = infoschema.PrimaryKeyType
				} else if idx.Unique {
					cname = idx.Name.O
					ctype = infoschema.UniqueKeyType
				} else {
					// The index has no constriant.
					continue
				}
				record := types.MakeDatums(
					infoschema.CatalogVal, // CONSTRAINT_CATALOG
					schema.Name.O,         // CONSTRAINT_SCHEMA
					cname,                 // CONSTRAINT_NAME
					schema.Name.O,         // TABLE_SCHEMA
					tbl.Name.O,            // TABLE_NAME
					ctype,                 // CONSTRAINT_TYPE
				)
				rows = append(rows, record)
			}
		}
	}
	e.rows = rows
}

// tableStorageStatsRetriever is used to read slow log data.
type tableStorageStatsRetriever struct {
	dummyCloser
	table         *model.TableInfo
	outputCols    []*model.ColumnInfo
	retrieved     bool
	initialized   bool
	extractor     *plannercore.TableStorageStatsExtractor
	initialTables []*initialTable
	curTable      int
	helper        *helper.Helper
	stats         helper.PDRegionStats
}

func (e *tableStorageStatsRetriever) retrieve(ctx context.Context, sctx sessionctx.Context) ([][]types.Datum, error) {
	if e.retrieved {
		return nil, nil
	}
	if !e.initialized {
		err := e.initialize(sctx)
		if err != nil {
			return nil, err
		}
	}
	if len(e.initialTables) == 0 || e.curTable >= len(e.initialTables) {
		e.retrieved = true
		return nil, nil
	}

	rows, err := e.setDataForTableStorageStats(sctx)
	if err != nil {
		return nil, err
	}
	if len(e.outputCols) == len(e.table.Columns) {
		return rows, nil
	}
	retRows := make([][]types.Datum, len(rows))
	for i, fullRow := range rows {
		row := make([]types.Datum, len(e.outputCols))
		for j, col := range e.outputCols {
			row[j] = fullRow[col.Offset]
		}
		retRows[i] = row
	}
	return retRows, nil
}

type initialTable struct {
	db string
	*model.TableInfo
}

func (e *tableStorageStatsRetriever) initialize(sctx sessionctx.Context) error {
	is := infoschema.GetInfoSchema(sctx)
	var databases []string
	schemas := e.extractor.TableSchema
	tables := e.extractor.TableName

	// If not specify the table_schema, return an error to avoid traverse all schemas and their tables.
	if len(schemas) == 0 {
		return errors.Errorf("Please specify the 'table_schema'")
	}

	// Filter the sys or memory schema.
	for schema := range schemas {
		if !util.IsMemOrSysDB(schema) {
			databases = append(databases, schema)
		}
	}

	// Extract the tables to the initialTable.
	for _, DB := range databases {
		// The user didn't specified the table, extract all tables of this db to initialTable.
		if len(tables) == 0 {
			tbs := is.SchemaTables(model.NewCIStr(DB))
			for _, tb := range tbs {
				e.initialTables = append(e.initialTables, &initialTable{DB, tb.Meta()})
			}
		} else {
			// The user specified the table, extract the specified tables of this db to initialTable.
			for tb := range tables {
				if tb, err := is.TableByName(model.NewCIStr(DB), model.NewCIStr(tb)); err == nil {
					e.initialTables = append(e.initialTables, &initialTable{DB, tb.Meta()})
				}
			}
		}
	}

	// Cache the helper and return an error if PD unavailable.
	tikvStore, ok := sctx.GetStore().(tikv.Storage)
	if !ok {
		return errors.Errorf("Information about TiKV region status can be gotten only when the storage is TiKV")
	}
	e.helper = helper.NewHelper(tikvStore)
	_, err := e.helper.GetPDAddr()
	if err != nil {
		return err
	}
	e.initialized = true
	return nil
}

func (e *tableStorageStatsRetriever) setDataForTableStorageStats(ctx sessionctx.Context) ([][]types.Datum, error) {
	rows := make([][]types.Datum, 0, 1024)
	count := 0
	for e.curTable < len(e.initialTables) && count < 1024 {
		table := e.initialTables[e.curTable]
		tableID := table.ID
		err := e.helper.GetPDRegionStats(tableID, &e.stats)
		if err != nil {
			return nil, err
		}
		peerCount := len(e.stats.StorePeerCount)

		record := types.MakeDatums(
			table.db,            // TABLE_SCHEMA
			table.Name.O,        // TABLE_NAME
			tableID,             // TABLE_ID
			peerCount,           // TABLE_PEER_COUNT
			e.stats.Count,       // TABLE_REGION_COUNT
			e.stats.EmptyCount,  // TABLE_EMPTY_REGION_COUNT
			e.stats.StorageSize, // TABLE_SIZE
			e.stats.StorageKeys, // TABLE_KEYS
		)
		rows = append(rows, record)
		count++
		e.curTable++
	}
	return rows, nil
}

func (e *memtableRetriever) setDataFromSessionVar(ctx sessionctx.Context) error {
	var rows [][]types.Datum
	var err error
	sessionVars := ctx.GetSessionVars()
	for _, v := range variable.SysVars {
		var value string
		value, err = variable.GetSessionSystemVar(sessionVars, v.Name)
		if err != nil {
			return err
		}
		row := types.MakeDatums(v.Name, value)
		rows = append(rows, row)
	}
	e.rows = rows
	return nil
}

// dataForAnalyzeStatusHelper is a helper function which can be used in show_stats.go
func dataForAnalyzeStatusHelper(sctx sessionctx.Context) (rows [][]types.Datum) {
	checker := privilege.GetPrivilegeManager(sctx)
	for _, job := range statistics.GetAllAnalyzeJobs() {
		job.Lock()
		var startTime interface{}
		if job.StartTime.IsZero() {
			startTime = nil
		} else {
			startTime = types.NewTime(types.FromGoTime(job.StartTime), mysql.TypeDatetime, 0)
		}
		if checker == nil || checker.RequestVerification(sctx.GetSessionVars().ActiveRoles, job.DBName, job.TableName, "", mysql.AllPrivMask) {
			rows = append(rows, types.MakeDatums(
				job.DBName,        // TABLE_SCHEMA
				job.TableName,     // TABLE_NAME
				job.PartitionName, // PARTITION_NAME
				job.JobInfo,       // JOB_INFO
				job.RowCount,      // ROW_COUNT
				startTime,         // START_TIME
				job.State,         // STATE
			))
		}
		job.Unlock()
	}
	return
}

// setDataForAnalyzeStatus gets all the analyze jobs.
func (e *memtableRetriever) setDataForAnalyzeStatus(sctx sessionctx.Context) {
	e.rows = dataForAnalyzeStatusHelper(sctx)
}

// setDataForPseudoProfiling returns pseudo data for table profiling when system variable `profiling` is set to `ON`.
func (e *memtableRetriever) setDataForPseudoProfiling(sctx sessionctx.Context) {
	if v, ok := sctx.GetSessionVars().GetSystemVar("profiling"); ok && variable.TiDBOptOn(v) {
		row := types.MakeDatums(
			0,                      // QUERY_ID
			0,                      // SEQ
			"",                     // STATE
			types.NewDecFromInt(0), // DURATION
			types.NewDecFromInt(0), // CPU_USER
			types.NewDecFromInt(0), // CPU_SYSTEM
			0,                      // CONTEXT_VOLUNTARY
			0,                      // CONTEXT_INVOLUNTARY
			0,                      // BLOCK_OPS_IN
			0,                      // BLOCK_OPS_OUT
			0,                      // MESSAGES_SENT
			0,                      // MESSAGES_RECEIVED
			0,                      // PAGE_FAULTS_MAJOR
			0,                      // PAGE_FAULTS_MINOR
			0,                      // SWAPS
			"",                     // SOURCE_FUNCTION
			"",                     // SOURCE_FILE
			0,                      // SOURCE_LINE
		)
		e.rows = append(e.rows, row)
	}
}

func (e *memtableRetriever) setDataForServersInfo() error {
	serversInfo, err := infosync.GetAllServerInfo(context.Background())
	if err != nil {
		return err
	}
	rows := make([][]types.Datum, 0, len(serversInfo))
	for _, info := range serversInfo {
		row := types.MakeDatums(
			info.ID,              // DDL_ID
			info.IP,              // IP
			int(info.Port),       // PORT
			int(info.StatusPort), // STATUS_PORT
			info.Lease,           // LEASE
			info.Version,         // VERSION
			info.GitHash,         // GIT_HASH
			info.BinlogStatus,    // BINLOG_STATUS
		)
		rows = append(rows, row)
	}
	e.rows = rows
	return nil
}

// todo can delete it after a full postgresQL compliant system table is implemented
func (e *memtableRetriever) setDataFromSequences(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if !table.IsSequence() {
				continue
			}
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			record := types.MakeDatums(
				infoschema.CatalogVal,     // TABLE_CATALOG
				schema.Name.O,             // TABLE_SCHEMA
				table.Name.O,              // TABLE_NAME
				table.Sequence.Cache,      // Cache
				table.Sequence.CacheValue, // CACHE_VALUE
				table.Sequence.Cycle,      // CYCLE
				table.Sequence.Increment,  // INCREMENT
				table.Sequence.MaxValue,   // MAXVALUE
				table.Sequence.MinValue,   // MINVALUE
				table.Sequence.Start,      // START
				table.Sequence.Comment,    // COMMENT
			)
			rows = append(rows, record)
		}
	}
	e.rows = rows
}

// dataForTableTiFlashReplica constructs data for table tiflash replica info.
func (e *memtableRetriever) dataForTableTiFlashReplica(ctx sessionctx.Context, schemas []*model.DBInfo) {
	var rows [][]types.Datum
	progressMap, err := infosync.GetTiFlashTableSyncProgress(context.Background())
	if err != nil {
		ctx.GetSessionVars().StmtCtx.AppendWarning(err)
	}
	for _, schema := range schemas {
		for _, tbl := range schema.Tables {
			if tbl.TiFlashReplica == nil {
				continue
			}
			progress := 1.0
			if !tbl.TiFlashReplica.Available {
				if pi := tbl.GetPartitionInfo(); pi != nil && len(pi.Definitions) > 0 {
					progress = 0
					for _, p := range pi.Definitions {
						if tbl.TiFlashReplica.IsPartitionAvailable(p.ID) {
							progress += 1
						} else {
							progress += progressMap[p.ID]
						}
					}
					progress = progress / float64(len(pi.Definitions))
				} else {
					progress = progressMap[tbl.ID]
				}
			}
			record := types.MakeDatums(
				schema.Name.O,                   // TABLE_SCHEMA
				tbl.Name.O,                      // TABLE_NAME
				tbl.ID,                          // TABLE_ID
				int64(tbl.TiFlashReplica.Count), // REPLICA_COUNT
				strings.Join(tbl.TiFlashReplica.LocationLabels, ","), // LOCATION_LABELS
				tbl.TiFlashReplica.Available,                         // AVAILABLE
				progress,                                             // PROGRESS
			)
			rows = append(rows, record)
		}
	}
	e.rows = rows
	return
}

func (e *memtableRetriever) setDataForStatementsSummary(ctx sessionctx.Context, tableName string) error {
	user := ctx.GetSessionVars().User
	isSuper := false
	if pm := privilege.GetPrivilegeManager(ctx); pm != nil {
		isSuper = pm.RequestVerificationWithUser("", "", "", mysql.SuperPriv, user)
	}
	switch tableName {
	case infoschema.TableStatementsSummary,
		infoschema.ClusterTableStatementsSummary:
		e.rows = stmtsummary.StmtSummaryByDigestMap.ToCurrentDatum(user, isSuper)
	case infoschema.TableStatementsSummaryHistory,
		infoschema.ClusterTableStatementsSummaryHistory:
		e.rows = stmtsummary.StmtSummaryByDigestMap.ToHistoryDatum(user, isSuper)
	}
	switch tableName {
	case infoschema.ClusterTableStatementsSummary,
		infoschema.ClusterTableStatementsSummaryHistory:
		rows, err := infoschema.AppendHostInfoToRows(e.rows)
		if err != nil {
			return err
		}
		e.rows = rows
	}
	return nil
}

// TiFlashSystemTableRetriever is used to read system table from tiflash.
type TiFlashSystemTableRetriever struct {
	dummyCloser
	table         *model.TableInfo
	outputCols    []*model.ColumnInfo
	instanceCount int
	instanceIdx   int
	instanceInfos []tiflashInstanceInfo
	rowIdx        int
	retrieved     bool
	initialized   bool
	extractor     *plannercore.TiFlashSystemTableExtractor
}

func (e *TiFlashSystemTableRetriever) retrieve(ctx context.Context, sctx sessionctx.Context) ([][]types.Datum, error) {
	if e.extractor.SkipRequest || e.retrieved {
		return nil, nil
	}
	if !e.initialized {
		err := e.initialize(sctx, e.extractor.TiFlashInstances)
		if err != nil {
			return nil, err
		}
	}
	if e.instanceCount == 0 || e.instanceIdx >= e.instanceCount {
		e.retrieved = true
		return nil, nil
	}

	for {
		rows, err := e.dataForTiFlashSystemTables(sctx, e.extractor.TiDBDatabases, e.extractor.TiDBTables)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 || e.instanceIdx >= e.instanceCount {
			return rows, nil
		}
	}
}

type tiflashInstanceInfo struct {
	id  string
	url string
}

func (e *TiFlashSystemTableRetriever) initialize(sctx sessionctx.Context, tiflashInstances set.StringSet) error {
	store := sctx.GetStore()
	if etcd, ok := store.(tikv.EtcdBackend); ok {
		var addrs []string
		var err error
		if addrs, err = etcd.EtcdAddrs(); err != nil {
			return err
		}
		if addrs != nil {
			domainFromCtx := domain.GetDomain(sctx)
			if domainFromCtx != nil {
				cli := domainFromCtx.GetEtcdClient()
				prefix := "/tiflash/cluster/http_port/"
				kv := clientv3.NewKV(cli)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				resp, err := kv.Get(ctx, prefix, clientv3.WithPrefix())
				cancel()
				if err != nil {
					return errors.Trace(err)
				}
				for _, ev := range resp.Kvs {
					id := string(ev.Key)[len(prefix):]
					if len(tiflashInstances) > 0 && !tiflashInstances.Exist(id) {
						continue
					}
					url := fmt.Sprintf("%s://%s", util.InternalHTTPSchema(), ev.Value)
					req, err := http.NewRequest(http.MethodGet, url, nil)
					if err != nil {
						return errors.Trace(err)
					}
					_, err = util.InternalHTTPClient().Do(req)
					if err != nil {
						sctx.GetSessionVars().StmtCtx.AppendWarning(err)
						continue
					}
					e.instanceInfos = append(e.instanceInfos, tiflashInstanceInfo{
						id:  id,
						url: url,
					})
					e.instanceCount += 1
				}
				e.initialized = true
				return nil
			}
		}
		return errors.Errorf("Etcd addrs not found")
	}
	return errors.Errorf("%T not an etcd backend", store)
}

func (e *TiFlashSystemTableRetriever) dataForTiFlashSystemTables(ctx sessionctx.Context, tidbDatabases string, tidbTables string) ([][]types.Datum, error) {
	var columnNames []string
	for _, c := range e.outputCols {
		if c.Name.O == "TIFLASH_INSTANCE" {
			continue
		}
		columnNames = append(columnNames, c.Name.L)
	}
	maxCount := 1024
	targetTable := strings.ToLower(strings.Replace(e.table.Name.O, "TIFLASH", "DT", 1))
	var filters []string
	if len(tidbDatabases) > 0 {
		filters = append(filters, fmt.Sprintf("tidb_database IN (%s)", strings.ReplaceAll(tidbDatabases, "\"", "'")))
	}
	if len(tidbTables) > 0 {
		filters = append(filters, fmt.Sprintf("tidb_table IN (%s)", strings.ReplaceAll(tidbTables, "\"", "'")))
	}
	sql := fmt.Sprintf("SELECT %s FROM system.%s", strings.Join(columnNames, ","), targetTable)
	if len(filters) > 0 {
		sql = fmt.Sprintf("%s WHERE %s", sql, strings.Join(filters, " AND "))
	}
	sql = fmt.Sprintf("%s LIMIT %d, %d", sql, e.rowIdx, maxCount)
	notNumber := "nan"
	instanceInfo := e.instanceInfos[e.instanceIdx]
	url := instanceInfo.url
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Trace(err)
	}
	q := req.URL.Query()
	q.Add("query", sql)
	req.URL.RawQuery = q.Encode()
	resp, err := util.InternalHTTPClient().Do(req)
	if err != nil {
		return nil, errors.Trace(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	terror.Log(resp.Body.Close())
	if err != nil {
		return nil, errors.Trace(err)
	}
	records := strings.Split(string(body), "\n")
	var rows [][]types.Datum
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		fields := strings.Split(record, "\t")
		if len(fields) < len(e.outputCols)-1 {
			return nil, errors.Errorf("Record from tiflash doesn't match schema %v", fields)
		}
		row := make([]types.Datum, len(e.outputCols))
		for index, column := range e.outputCols {
			if column.Name.O == "TIFLASH_INSTANCE" {
				continue
			}
			if column.Tp == mysql.TypeVarchar {
				row[index].SetString(fields[index], mysql.DefaultCollationName)
			} else if column.Tp == mysql.TypeLonglong {
				if fields[index] == notNumber {
					continue
				}
				value, err := strconv.ParseInt(fields[index], 10, 64)
				if err != nil {
					return nil, errors.Trace(err)
				}
				row[index].SetInt64(value)
			} else if column.Tp == mysql.TypeDouble {
				if fields[index] == notNumber {
					continue
				}
				value, err := strconv.ParseFloat(fields[index], 64)
				if err != nil {
					return nil, errors.Trace(err)
				}
				row[index].SetFloat64(value)
			} else {
				return nil, errors.Errorf("Meet column of unknown type %v", column)
			}
		}
		row[len(e.outputCols)-1].SetString(instanceInfo.id, mysql.DefaultCollationName)
		rows = append(rows, row)
	}
	e.rowIdx += len(rows)
	if len(rows) < maxCount {
		e.instanceIdx += 1
		e.rowIdx = 0
	}
	return rows, nil
}

type hugeMemTableRetriever struct {
	dummyCloser
	table       *model.TableInfo
	columns     []*model.ColumnInfo
	retrieved   bool
	initialized bool
	rows        [][]types.Datum
	dbs         []*model.DBInfo
	dbsIdx      int
	tblIdx      int
}

// retrieve implements the infoschemaRetriever interface
func (e *hugeMemTableRetriever) retrieve(ctx context.Context, sctx sessionctx.Context) ([][]types.Datum, error) {
	if e.retrieved {
		return nil, nil
	}

	if !e.initialized {
		is := infoschema.GetInfoSchema(sctx)
		dbs := is.AllSchemas()
		sort.Sort(infoschema.SchemasSorter(dbs))
		e.dbs = dbs
		e.initialized = true
		e.rows = make([][]types.Datum, 0, 1024)
	}

	var err error
	switch e.table.Name.O {
	case infoschema.TableColumns:
		err = e.setDataForColumns(sctx)
	case infoschema.TablePgColumns:
		err = e.setDataForPgColumns(sctx)
	}
	if err != nil {
		return nil, err
	}
	e.retrieved = len(e.rows) == 0

	return adjustColumns(e.rows, e.columns, e.table), nil
}

func adjustColumns(input [][]types.Datum, outColumns []*model.ColumnInfo, table *model.TableInfo) [][]types.Datum {
	if len(outColumns) == len(table.Columns) {
		return input
	}
	rows := make([][]types.Datum, len(input))
	for i, fullRow := range input {
		row := make([]types.Datum, len(outColumns))
		for j, col := range outColumns {
			row[j] = fullRow[col.Offset]
		}
		rows[i] = row
	}
	return rows
}

type pgMemTableRetriever struct {
	dummyCloser
	table       *model.TableInfo
	columns     []*model.ColumnInfo
	retrieved   bool
	initialized bool
	rows        [][]types.Datum
	dbs         []*model.DBInfo
	dbsIdx      int
	tblIdx      int
	rowIdx      int
}

func (e *pgMemTableRetriever) retrieve(ctx context.Context, sctx sessionctx.Context) ([][]types.Datum, error) {
	if e.retrieved {
		return nil, nil
	}
	//Cache the ret full rows in schemataRetriever
	if !e.initialized {
		is := infoschema.GetInfoSchema(sctx)
		dbs := is.AllSchemas()
		sort.Sort(infoschema.SchemasSorter(dbs))
		var err error
		switch e.table.Name.O {
		case infoschema.TablePgInformationsSchemaCatalogName:
			e.setDataForPgInformationSchemaCatalogName()
		case infoschema.TablePgSchemata:
			e.setDataForPgSchemata(sctx, dbs)
		case infoschema.TablePgAdministrableRoleAuthorizations:
			e.setDataForPgAdministrableRoleAuthorizations()
		case infoschema.TablePgEnabledRoles:
			e.setDataForPgEnabledRoles()
		case infoschema.TablePgTables:
			err = e.setDataForPgTables(sctx, dbs)
		case infoschema.TablePgCollations:
			e.setDataForPgCollations()
		case infoschema.TablePgSequences:
			e.setDataForPgSequences(sctx, dbs)
		case infoschema.TablePgViews:
			e.setDataForPgViews(sctx, dbs)
		case infoschema.TablePgTableConstraints:
			e.setDataForPgTableConstraints(sctx, dbs)
		case infoschema.TablePgCharacterSets:
			e.setDataForPgCharacterSets()
		case infoschema.TablePgKeyColumnUsage:
			e.setDataForPgKeyColumnUsage(sctx, dbs)
		case infoschema.TablePgCollationCharacterSetApplicability:
			e.SetDataForCollationCharacterSetApplicability()
			// todo set data for tables
		}
		if err != nil {
			return nil, err
		}
		e.initialized = true
	}

	//Adjust the amount of each return
	maxCount := 1024
	retCount := maxCount
	if e.rowIdx+maxCount > len(e.rows) {
		retCount = len(e.rows) - e.rowIdx
		e.retrieved = true
	}
	ret := make([][]types.Datum, retCount)
	for i := e.rowIdx; i < e.rowIdx+retCount; i++ {
		ret[i-e.rowIdx] = e.rows[i]
	}
	e.rowIdx += retCount
	return adjustColumns(ret, e.columns, e.table), nil
}

// setDataForPgInformationSchemaCatalogName set data for pgTable information_schema_catalog_name
// todo 这个值应该是动态的，这里先写死
func (e *pgMemTableRetriever) setDataForPgInformationSchemaCatalogName() {
	var rows [][]types.Datum
	rows = append(rows,
		types.MakeDatums(
			"postgres", // catalog_name
		),
	)
	e.rows = rows
}

// setDataForPgAdministrableRoleAuthorizations set data for pgTable administrable_role_authorizations
func (e *pgMemTableRetriever) setDataForPgAdministrableRoleAuthorizations() {
	var rows [][]types.Datum
	rows = append(rows,
		types.MakeDatums(
			"root",  // grantee
			"admin", // role_name
			"YES",   // is_grantable
		),
	)
	e.rows = rows
}

// setDataForPgSchemata set data for pgTable schemata
// todo 这里的 catalog_name 和 schema_owner 应该是动态获取，暂时写死
func (e *pgMemTableRetriever) setDataForPgSchemata(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	rows := make([][]types.Datum, 0, len(schemas))

	for _, schema := range schemas {

		charset := mysql.DefaultCharset
		collation := mysql.DefaultCollationName

		if len(schema.Charset) > 0 {
			charset = schema.Charset //overwrite default
		}

		if len(schema.Collate) > 0 {
			collation = schema.Collate //overwrite default
		}

		if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, "", "", mysql.AllPrivMask) {
			continue
		}

		record := types.MakeDatums(
			"postgres",    // catalog_name
			schema.Name.O, // schema_name
			"root",        // schema_owner
			nil,           // default_character_set_catalog
			nil,           // default_character_set_schema
			charset,       // default_character_set_name
			nil,           // sql_path
			collation,     // DEFAULT_COLLATION_NAME
		)
		rows = append(rows, record)
	}
	e.rows = rows
}

// setDataForPgTables set data for pgTable tables
func (e *pgMemTableRetriever) setDataForPgTables(ctx sessionctx.Context, schemas []*model.DBInfo) error {
	tableRowsMap, colLengthMap, err := tableStatsCache.get(ctx)
	if err != nil {
		return err
	}

	checker := privilege.GetPrivilegeManager(ctx)

	var rows [][]types.Datum
	createTimeTp := mysql.TypeDatetime
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			collation := table.Collate
			if collation == "" {
				collation = mysql.DefaultCollationName
			}
			createTime := types.NewTime(types.FromGoTime(table.GetUpdateTime()), createTimeTp, types.DefaultFsp)

			createOptions := ""

			if table.IsSequence() {
				continue
			}

			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			if !table.IsView() {
				if table.GetPartitionInfo() != nil {
					createOptions = "partitioned"
				}
				var autoIncID interface{}
				hasAutoincID, _ := infoschema.HasAutoIncrementColumn(table)
				if hasAutoincID {
					autoIncID, err = getAutoIncrementID(ctx, schema, table)
					if err != nil {
						return err
					}
				}
				var rowCount, dataLength, indexLength uint64
				if table.GetPartitionInfo() == nil {
					rowCount = tableRowsMap[table.ID]
					dataLength, indexLength = getDataAndIndexLength(table, table.ID, rowCount, colLengthMap)
				} else {
					for _, pi := range table.GetPartitionInfo().Definitions {
						rowCount += tableRowsMap[pi.ID]
						parDataLen, parIndexLen := getDataAndIndexLength(table, pi.ID, tableRowsMap[pi.ID], colLengthMap)
						dataLength += parDataLen
						indexLength += parIndexLen
					}
				}
				avgRowLength := uint64(0)
				if rowCount != 0 {
					avgRowLength = dataLength / rowCount
				}
				var tableType string
				switch schema.Name.L {
				case util.InformationSchemaName.L, util.PerformanceSchemaName.L,
					util.MetricSchemaName.L:
					tableType = "SYSTEM VIEW"
				default:
					tableType = "BASE TABLE"
				}
				shardingInfo := infoschema.GetShardingInfo(schema, table)
				record := types.MakeDatums(
					"postgres",    // TABLE_CATALOG
					schema.Name.O, // TABLE_SCHEMA
					table.Name.O,  // TABLE_NAME
					tableType,     // TABLE_TYPE
					nil,           // self_referencing_column_name
					nil,           // reference_generation
					nil,           // user_defined_type_catalog
					nil,           // user_defined_type_schema
					nil,           // user_defined_type_name
					nil,           // is_insertable_into
					nil,           // is_typed
					nil,           // commit_action
					"InnoDB",      // ENGINE
					uint64(10),    // VERSION
					"Compact",     // ROW_FORMAT
					rowCount,      // TABLE_ROWS
					avgRowLength,  // AVG_ROW_LENGTH
					dataLength,    // DATA_LENGTH
					uint64(0),     // MAX_DATA_LENGTH
					indexLength,   // INDEX_LENGTH
					uint64(0),     // DATA_FREE
					autoIncID,     // AUTO_INCREMENT
					createTime,    // CREATE_TIME
					nil,           // UPDATE_TIME
					nil,           // CHECK_TIME
					collation,     // TABLE_COLLATION
					nil,           // CHECKSUM
					createOptions, // CREATE_OPTIONS
					table.Comment, // TABLE_COMMENT
					table.ID,      // TIDB_TABLE_ID
					shardingInfo,  // TIDB_ROW_ID_SHARDING_INFO
				)
				rows = append(rows, record)
			} else {
				record := types.MakeDatums(
					"postgres",    // TABLE_CATALOG
					schema.Name.O, // TABLE_SCHEMA
					table.Name.O,  // TABLE_NAME
					"VIEW",        // TABLE_TYPE
					nil,           // self_referencing_column_name
					nil,           // reference_generation
					nil,           // user_defined_type_catalog
					nil,           // user_defined_type_schema
					nil,           // user_defined_type_name
					nil,           // is_insertable_into
					nil,           // is_typed
					nil,           // commit_action
					nil,           // ENGINE
					nil,           // VERSION
					nil,           // ROW_FORMAT
					nil,           // TABLE_ROWS
					nil,           // AVG_ROW_LENGTH
					nil,           // DATA_LENGTH
					nil,           // MAX_DATA_LENGTH
					nil,           // INDEX_LENGTH
					nil,           // DATA_FREE
					nil,           // AUTO_INCREMENT
					createTime,    // CREATE_TIME
					nil,           // UPDATE_TIME
					nil,           // CHECK_TIME
					nil,           // TABLE_COLLATION
					nil,           // CHECKSUM
					nil,           // CREATE_OPTIONS
					"VIEW",        // TABLE_COMMENT
					table.ID,      // TIDB_TABLE_ID
					nil,           // TIDB_ROW_ID_SHARDING_INFO
				)
				rows = append(rows, record)
			}
		}
	}
	e.rows = rows
	return nil
}

// setDataForPgEnabledRoles set data for pgTable enabled_roles
func (e *pgMemTableRetriever) setDataForPgEnabledRoles() {
	var rows [][]types.Datum
	rows = append(rows,
		types.MakeDatums(
			"root", // catalog_name
		),
	)
	rows = append(rows,
		types.MakeDatums(
			"admin", // catalog_name
		),
	)
	e.rows = rows
}

// setDataForPgCollations set data for pgTable collations
func (e *pgMemTableRetriever) setDataForPgCollations() {
	var rows [][]types.Datum
	collations := collate.GetSupportedCollations()
	for _, collation := range collations {
		isDefault := ""
		if collation.IsDefault {
			isDefault = "Yes"
		}
		rows = append(rows,
			types.MakeDatums("postgres", "pg_catalog", collation.Name, "NO PAD", collation.CharsetName, collation.ID, isDefault, "Yes", 1),
		)
	}
	e.rows = rows
}

// setDataForPgColumns set data for pgTable Columns
func (e *hugeMemTableRetriever) setDataForPgColumns(ctx sessionctx.Context) error {
	checker := privilege.GetPrivilegeManager(ctx)
	e.rows = e.rows[:0]
	batch := 1024
	for ; e.dbsIdx < len(e.dbs); e.dbsIdx++ {
		schema := e.dbs[e.dbsIdx]
		for e.tblIdx < len(schema.Tables) {
			table := schema.Tables[e.tblIdx]
			e.tblIdx++
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			e.dataForPgColumnsInTable(schema, table)
			if len(e.rows) >= batch {
				return nil
			}
		}
		e.tblIdx = 0
	}
	return nil
}

// dataForPgColumnsInTable get pgTable pg_columns data from system
func (e *hugeMemTableRetriever) dataForPgColumnsInTable(schema *model.DBInfo, tbl *model.TableInfo) {
	for i, col := range tbl.Columns {
		if col.Hidden {
			continue
		}
		var charMaxLen, charOctLen, numericPrecision, numericScale, datetimePrecision interface{}
		colLen, decimal := col.Flen, col.Decimal
		defaultFlen, defaultDecimal := mysql.GetDefaultFieldLengthAndDecimal(col.Tp)
		if decimal == types.UnspecifiedLength {
			decimal = defaultDecimal
		}
		if colLen == types.UnspecifiedLength {
			colLen = defaultFlen
		}
		if col.Tp == mysql.TypeSet {
			// Example: In MySQL set('a','bc','def','ghij') has length 13, because
			// len('a')+len('bc')+len('def')+len('ghij')+len(ThreeComma)=13
			// Reference link: https://bugs.mysql.com/bug.php?id=22613
			colLen = 0
			for _, ele := range col.Elems {
				colLen += len(ele)
			}
			if len(col.Elems) != 0 {
				colLen += (len(col.Elems) - 1)
			}
			charMaxLen = colLen
			charOctLen = colLen
		} else if col.Tp == mysql.TypeEnum {
			// Example: In MySQL enum('a', 'ab', 'cdef') has length 4, because
			// the longest string in the enum is 'cdef'
			// Reference link: https://bugs.mysql.com/bug.php?id=22613
			colLen = 0
			for _, ele := range col.Elems {
				if len(ele) > colLen {
					colLen = len(ele)
				}
			}
			charMaxLen = colLen
			charOctLen = colLen
		} else if types.IsString(col.Tp) {
			charMaxLen = colLen
			charOctLen = colLen
		} else if types.IsTypeFractionable(col.Tp) {
			datetimePrecision = decimal
		} else if types.IsTypeNumeric(col.Tp) {
			numericPrecision = colLen
			if col.Tp != mysql.TypeFloat && col.Tp != mysql.TypeDouble {
				numericScale = decimal
			} else if decimal != -1 {
				numericScale = decimal
			}
		}
		columnType := col.FieldType.InfoSchemaStr()
		columnDesc := table.NewColDesc(table.ToColumn(col))
		var columnDefault interface{}
		if columnDesc.DefaultValue != nil {
			columnDefault = fmt.Sprintf("%v", columnDesc.DefaultValue)
		}
		var record []types.Datum
		if schema.Name.O == "information_schema" {
			record = types.MakeDatums(
				"postgres",                           // TABLE_CATALOG
				schema.Name.O,                        // TABLE_SCHEMA
				tbl.Name.O,                           // TABLE_NAME
				col.Name.O,                           // COLUMN_NAME
				i+1,                                  // ORIGINAL_POSITION
				columnDefault,                        // COLUMN_DEFAULT
				columnDesc.Null,                      // IS_NULLABLE
				types.TypeToStr(col.Tp, col.Charset), // DATA_TYPE
				charMaxLen,                           // CHARACTER_MAXIMUM_LENGTH
				charOctLen,                           // CHARACTER_OCTET_LENGTH
				numericPrecision,                     // NUMERIC_PRECISION
				nil,                                  // numeric_precision_radix
				numericScale,                         // NUMERIC_SCALE
				datetimePrecision,                    // DATETIME_PRECISION
				nil,                                  // interval_type
				nil,                                  // interval_precision
				nil,                                  // character_set_catalog
				nil,                                  // character_set_schema
				columnDesc.Charset,                   // CHARACTER_SET_NAME
				"postgres",                           // collation_catalog
				"pg_catalog",                         // collation_schema
				columnDesc.Collation,                 // COLLATION_NAME
				"postgres",                           // domain_catalog
				"information_schema",                 // domain_schema
				"sql_identifier",                     // domain_name
				"postgres",                           // udt_catalog
				"pg_catalog",                         // udt_schema
				nil,                                  // udt_name
				nil,                                  // scope_catalog
				nil,                                  // scope_schema
				nil,                                  // scope_name
				nil,                                  // maximum_cardinality
				nil,                                  // dtd_identifier
				nil,                                  // is_self_referencing
				nil,                                  // is_identity
				nil,                                  // identity_generation
				nil,                                  // identity_start
				nil,                                  // identity_increment
				nil,                                  // identity_maximum
				nil,                                  // identity_minimum
				nil,                                  // identity_cycle
				nil,                                  // is_generated
				col.GeneratedExprString,              // GENERATION_EXPRESSION
				nil,                                  // is_updatable
				columnType,                           // COLUMN_TYPE
				columnDesc.Key,                       // COLUMN_KEY
				columnDesc.Extra,                     // EXTRA
				"select,insert,update,references",    // PRIVILEGES
				columnDesc.Comment,                   // COLUMN_COMMENT
			)
		} else {
			record = types.MakeDatums(
				"postgres",                           // TABLE_CATALOG
				schema.Name.O,                        // TABLE_SCHEMA
				tbl.Name.O,                           // TABLE_NAME
				col.Name.O,                           // COLUMN_NAME
				i+1,                                  // ORIGINAL_POSITION
				columnDefault,                        // COLUMN_DEFAULT
				columnDesc.Null,                      // IS_NULLABLE
				types.TypeToStr(col.Tp, col.Charset), // DATA_TYPE
				charMaxLen,                           // CHARACTER_MAXIMUM_LENGTH
				charOctLen,                           // CHARACTER_OCTET_LENGTH
				numericPrecision,                     // NUMERIC_PRECISION
				nil,                                  // numeric_precision_radix
				numericScale,                         // NUMERIC_SCALE
				datetimePrecision,                    // DATETIME_PRECISION
				nil,                                  // interval_type
				nil,                                  // interval_precision
				nil,                                  // character_set_catalog
				nil,                                  // character_set_schema
				columnDesc.Charset,                   // CHARACTER_SET_NAME
				nil,                                  // collation_catalog
				nil,                                  // collation_schema
				columnDesc.Collation,                 // COLLATION_NAME
				nil,                                  // domain_catalog
				nil,                                  // domain_schema
				nil,                                  // domain_name
				"postgres",                           // udt_catalog
				"pg_catalog",                         // udt_schema
				nil,                                  // udt_name
				nil,                                  // scope_catalog
				nil,                                  // scope_schema
				nil,                                  // scope_name
				nil,                                  // maximum_cardinality
				nil,                                  // dtd_identifier
				nil,                                  // is_self_referencing
				nil,                                  // is_identity
				nil,                                  // identity_generation
				nil,                                  // identity_start
				nil,                                  // identity_increment
				nil,                                  // identity_maximum
				nil,                                  // identity_minimum
				nil,                                  // identity_cycle
				nil,                                  // is_generated
				col.GeneratedExprString,              // GENERATION_EXPRESSION
				nil,                                  // is_updatable
				columnType,                           // COLUMN_TYPE
				columnDesc.Key,                       // COLUMN_KEY
				columnDesc.Extra,                     // EXTRA
				"select,insert,update,references",    // PRIVILEGES
				columnDesc.Comment,                   // COLUMN_COMMENT
			)
		}
		e.rows = append(e.rows, record)
	}
}

// setDataForPgSequences set data for pgTable sequences
func (e *pgMemTableRetriever) setDataForPgSequences(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if !table.IsSequence() {
				continue
			}
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			record := types.MakeDatums(
				"postgres",                // sequence_catalog
				schema.Name.O,             // sequence_schema
				table.Name.O,              // sequence_name
				nil,                       // data_type
				nil,                       // numeric_precision
				nil,                       // numeric_precision_radix
				nil,                       // numeric_scale
				nil,                       // start_value
				nil,                       // minimum_value
				nil,                       // maximum_value
				table.Sequence.Increment,  // INCREMENT
				nil,                       // cycle_option
				"postgres",                // TABLE_CATALOG
				table.Sequence.Cache,      // Cache
				table.Sequence.CacheValue, // CACHE_VALUE
				table.Sequence.Cycle,      // CYCLE
				table.Sequence.MaxValue,   // MAX_VALUE
				table.Sequence.MinValue,   // MIN_VALUE
				table.Sequence.Start,      // START
				table.Sequence.Comment,    // COMMENT
			)
			rows = append(rows, record)
		}
	}
	e.rows = rows
}

// setDataForPgViews set data for pgTable pg_views
func (e *pgMemTableRetriever) setDataForPgViews(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if !table.IsView() {
				continue
			}
			collation := table.Collate
			charset := table.Charset
			if collation == "" {
				collation = mysql.DefaultCollationName
			}
			if charset == "" {
				charset = mysql.DefaultCharset
			}
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			record := types.MakeDatums(
				"postgres",                      // TABLE_CATALOG
				schema.Name.O,                   // TABLE_SCHEMA
				table.Name.O,                    // TABLE_NAME
				table.View.SelectStmt,           // VIEW_DEFINITION
				table.View.CheckOption.String(), // CHECK_OPTION
				"NO",                            // IS_UPDATABLE
				nil,                             // is_insertable_into
				nil,                             // is_trigger_updatable
				nil,                             // is_trigger_deletable
				nil,                             // is_trigger_insertable_into
				table.View.Definer.String(),     // DEFINER
				table.View.Security.String(),    // SECURITY_TYPE
				charset,                         // CHARACTER_SET_CLIENT
				collation,                       // COLLATION_CONNECTION
			)
			rows = append(rows, record)
		}
	}
	e.rows = rows
}

// setDataForPgTableConstraints set data for pgTable table_constraints
func (e *pgMemTableRetriever) setDataForPgTableConstraints(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	var rows [][]types.Datum
	for _, schema := range schemas {
		for _, tbl := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, tbl.Name.L, "", mysql.AllPrivMask) {
				continue
			}

			if tbl.PKIsHandle {
				record := types.MakeDatums(
					"postgres",                // CONSTRAINT_CATALOG
					schema.Name.O,             // CONSTRAINT_SCHEMA
					mysql.PrimaryKeyName,      // CONSTRAINT_NAME
					"postgres",                // table_catalog
					schema.Name.O,             // TABLE_SCHEMA
					tbl.Name.O,                // TABLE_NAME
					infoschema.PrimaryKeyType, // CONSTRAINT_TYPE
					nil,                       // is_deferrable
					nil,                       // initially_deferred
					nil,                       // enforced
				)
				rows = append(rows, record)
			}

			for _, idx := range tbl.Indices {
				var cname, ctype string
				if idx.Primary {
					cname = mysql.PrimaryKeyName
					ctype = infoschema.PrimaryKeyType
				} else if idx.Unique {
					cname = idx.Name.O
					ctype = infoschema.UniqueKeyType
				} else {
					// The index has no constriant.
					continue
				}
				record := types.MakeDatums(
					"postgres",    // CONSTRAINT_CATALOG
					schema.Name.O, // CONSTRAINT_SCHEMA
					cname,         // CONSTRAINT_NAME
					"postgres",    // table_catalog
					schema.Name.O, // TABLE_SCHEMA
					tbl.Name.O,    // TABLE_NAME
					ctype,         // CONSTRAINT_TYPE
					nil,           // is_deferrable
					nil,           // initially_deferred
					nil,           // enforced
				)
				rows = append(rows, record)
			}
		}
	}
	e.rows = rows
}

// setDataForPgCharacterSets set data for pgTable character_sets
func (e *pgMemTableRetriever) setDataForPgCharacterSets() {
	var rows [][]types.Datum
	charsets := charset.GetSupportedCharsets()
	for _, charset := range charsets {
		rows = append(rows,
			types.MakeDatums(nil, nil, charset.Name, "UCS", charset.Name, "postgres", nil, charset.DefaultCollation, charset.Desc, charset.Maxlen),
		)
	}
	e.rows = rows
}

// SetDataForCollationCharacterSetApplicability set data for pgTable collation_character_set_applicability
func (e *pgMemTableRetriever) SetDataForCollationCharacterSetApplicability() {
	var rows [][]types.Datum
	collations := collate.GetSupportedCollations()
	for _, collation := range collations {
		rows = append(rows,
			types.MakeDatums("postgres", "pg_catalog", collation.Name, nil, nil, collation.CharsetName),
		)
	}
	e.rows = rows
}

// setDataForPgKeyColumnUsage set data for pgTable KEY_COLUMN_USAGE
func (e *pgMemTableRetriever) setDataForPgKeyColumnUsage(ctx sessionctx.Context, schemas []*model.DBInfo) {
	checker := privilege.GetPrivilegeManager(ctx)
	rows := make([][]types.Datum, 0, len(schemas)) // The capacity is not accurate, but it is not a big problem.
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			if checker != nil && !checker.RequestVerification(ctx.GetSessionVars().ActiveRoles, schema.Name.L, table.Name.L, "", mysql.AllPrivMask) {
				continue
			}
			rs := pgKeyColumnUsageInTable(schema, table)
			rows = append(rows, rs...)
		}
	}
	e.rows = rows
}

// pgKeyColumnUsageInTable get table KEY_COLUMN_USAGE data from system
func pgKeyColumnUsageInTable(schema *model.DBInfo, table *model.TableInfo) [][]types.Datum {
	var rows [][]types.Datum
	if table.PKIsHandle {
		for _, col := range table.Columns {
			if mysql.HasPriKeyFlag(col.Flag) {
				record := types.MakeDatums(
					"postgres",                   // CONSTRAINT_CATALOG
					schema.Name.O,                // CONSTRAINT_SCHEMA
					infoschema.PrimaryConstraint, // CONSTRAINT_NAME
					"postgres",                   // TABLE_CATALOG
					schema.Name.O,                // TABLE_SCHEMA
					table.Name.O,                 // TABLE_NAME
					col.Name.O,                   // COLUMN_NAME
					1,                            // ORDINAL_POSITION
					1,                            // POSITION_IN_UNIQUE_CONSTRAINT
					nil,                          // REFERENCED_TABLE_SCHEMA
					nil,                          // REFERENCED_TABLE_NAME
					nil,                          // REFERENCED_COLUMN_NAME
				)
				rows = append(rows, record)
				break
			}
		}
	}
	nameToCol := make(map[string]*model.ColumnInfo, len(table.Columns))
	for _, c := range table.Columns {
		nameToCol[c.Name.L] = c
	}
	for _, index := range table.Indices {
		var idxName string
		if index.Primary {
			idxName = infoschema.PrimaryConstraint
		} else if index.Unique {
			idxName = index.Name.O
		} else {
			// Only handle unique/primary key
			continue
		}
		for i, key := range index.Columns {
			col := nameToCol[key.Name.L]
			record := types.MakeDatums(
				"postgres",    // CONSTRAINT_CATALOG
				schema.Name.O, // CONSTRAINT_SCHEMA
				idxName,       // CONSTRAINT_NAME
				"postgres",    // TABLE_CATALOG
				schema.Name.O, // TABLE_SCHEMA
				table.Name.O,  // TABLE_NAME
				col.Name.O,    // COLUMN_NAME
				i+1,           // ORDINAL_POSITION,
				nil,           // POSITION_IN_UNIQUE_CONSTRAINT
				nil,           // REFERENCED_TABLE_SCHEMA
				nil,           // REFERENCED_TABLE_NAME
				nil,           // REFERENCED_COLUMN_NAME
			)
			rows = append(rows, record)
		}
	}
	for _, fk := range table.ForeignKeys {
		fkRefCol := ""
		if len(fk.RefCols) > 0 {
			fkRefCol = fk.RefCols[0].O
		}
		for i, key := range fk.Cols {
			col := nameToCol[key.L]
			record := types.MakeDatums(
				"postgres",    // CONSTRAINT_CATALOG
				schema.Name.O, // CONSTRAINT_SCHEMA
				fk.Name.O,     // CONSTRAINT_NAME
				"postgres",    // TABLE_CATALOG
				schema.Name.O, // TABLE_SCHEMA
				table.Name.O,  // TABLE_NAME
				col.Name.O,    // COLUMN_NAME
				i+1,           // ORDINAL_POSITION,
				1,             // POSITION_IN_UNIQUE_CONSTRAINT
				schema.Name.O, // REFERENCED_TABLE_SCHEMA
				fk.RefTable.O, // REFERENCED_TABLE_NAME
				fkRefCol,      // REFERENCED_COLUMN_NAME
			)
			rows = append(rows, record)
		}
	}
	return rows
}
