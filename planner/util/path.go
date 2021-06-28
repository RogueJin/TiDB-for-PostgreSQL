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

package util

import (
	"github.com/DigitalChinaOpenSource/DCParser/ast"
	"github.com/DigitalChinaOpenSource/DCParser/model"
	"github.com/pingcap/tidb/expression"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/sessionctx"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/collate"
	"github.com/pingcap/tidb/util/ranger"
)

// AccessPath indicates the way we access a table: by using single index, or by using multiple indexes,
// or just by using table scan.
type AccessPath struct {
	Index          *model.IndexInfo
	FullIdxCols    []*expression.Column
	FullIdxColLens []int
	IdxCols        []*expression.Column
	IdxColLens     []int
	Ranges         []*ranger.Range
	// CountAfterAccess is the row count after we apply range seek and before we use other filter to filter data.
	CountAfterAccess float64
	// CountAfterIndex is the row count after we apply filters on index and before we apply the table filters.
	CountAfterIndex float64
	AccessConds     []expression.Expression
	EqCondCount     int
	EqOrInCondCount int
	IndexFilters    []expression.Expression
	TableFilters    []expression.Expression
	// PartialIndexPaths store all index access paths.
	// If there are extra filters, store them in TableFilters.
	PartialIndexPaths []*AccessPath

	StoreType kv.StoreType

	IsDNFCond bool

	// IsTablePath indicates whether this path is table path.
	IsTablePath bool
	// IsTiFlashGlobalRead indicates whether this path is a remote read path for tiflash
	IsTiFlashGlobalRead bool

	// Forced means this path is generated by `use/force index()`.
	Forced bool

	PkCol *expression.Column
}

// SplitCorColAccessCondFromFilters move the necessary filter in the form of index_col = corrlated_col to access conditions.
func (path *AccessPath) SplitCorColAccessCondFromFilters(ctx sessionctx.Context, eqOrInCount int) (access, remained []expression.Expression) {
	access = make([]expression.Expression, len(path.IdxCols)-eqOrInCount)
	used := make([]bool, len(path.TableFilters))
	for i := eqOrInCount; i < len(path.IdxCols); i++ {
		matched := false
		for j, filter := range path.TableFilters {
			if used[j] || !isColEqCorColOrConstant(ctx, filter, path.IdxCols[i]) {
				continue
			}
			matched = true
			access[i-eqOrInCount] = filter
			if path.IdxColLens[i] == types.UnspecifiedLength {
				used[j] = true
			}
			break
		}
		if !matched {
			access = access[:i-eqOrInCount]
			break
		}
	}
	for i, ok := range used {
		if !ok {
			remained = append(remained, path.TableFilters[i])
		}
	}
	return access, remained
}

// isColEqCorColOrConstant checks if the expression is a eq function that one side is constant or correlated column
// and another is column.
func isColEqCorColOrConstant(ctx sessionctx.Context, filter expression.Expression, col *expression.Column) bool {
	f, ok := filter.(*expression.ScalarFunction)
	if !ok || f.FuncName.L != ast.EQ {
		return false
	}
	_, collation := f.CharsetAndCollation(ctx)
	if c, ok := f.GetArgs()[0].(*expression.Column); ok {
		if c.RetType.EvalType() == types.ETString && !collate.CompatibleCollate(collation, c.RetType.Collate) {
			return false
		}
		if _, ok := f.GetArgs()[1].(*expression.Constant); ok {
			if col.Equal(nil, c) {
				return true
			}
		}
		if _, ok := f.GetArgs()[1].(*expression.CorrelatedColumn); ok {
			if col.Equal(nil, c) {
				return true
			}
		}
	}
	if c, ok := f.GetArgs()[1].(*expression.Column); ok {
		if c.RetType.EvalType() == types.ETString && !collate.CompatibleCollate(collation, c.RetType.Collate) {
			return false
		}
		if _, ok := f.GetArgs()[0].(*expression.Constant); ok {
			if col.Equal(nil, c) {
				return true
			}
		}
		if _, ok := f.GetArgs()[0].(*expression.CorrelatedColumn); ok {
			if col.Equal(nil, c) {
				return true
			}
		}
	}
	return false
}
