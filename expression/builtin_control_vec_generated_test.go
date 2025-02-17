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

// Code generated by go generate in expression/generator; DO NOT EDIT.

package expression

import (
	"math/rand"
	"testing"

	"github.com/DigitalChinaOpenSource/DCParser/ast"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/types"
)

var defaultControlIntGener = &controlIntGener{zeroRation: 0.3, defaultGener: *newDefaultGener(0.3, types.ETInt)}

type controlIntGener struct {
	zeroRation float64
	defaultGener
}

func (g *controlIntGener) gen() interface{} {
	if rand.Float64() < g.zeroRation {
		return int64(0)
	}
	return g.defaultGener.gen()
}

var vecBuiltinControlCases = map[string][]vecExprBenchCase{

	ast.Case: {

		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt, types.ETInt}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt, types.ETInt, types.ETInt}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt, types.ETInt, types.ETInt, types.ETInt}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETInt, types.ETReal}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETInt, types.ETReal, types.ETReal}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETInt, types.ETReal, types.ETInt, types.ETReal}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETInt, types.ETReal, types.ETInt, types.ETReal, types.ETReal}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETInt, types.ETDecimal}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETInt, types.ETDecimal, types.ETDecimal}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETInt, types.ETDecimal, types.ETInt, types.ETDecimal}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETInt, types.ETDecimal, types.ETInt, types.ETDecimal, types.ETDecimal}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETInt, types.ETString}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETInt, types.ETString, types.ETString}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETInt, types.ETString, types.ETInt, types.ETString}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETInt, types.ETString, types.ETInt, types.ETString, types.ETString}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETInt, types.ETDatetime}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETInt, types.ETDatetime, types.ETDatetime}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETInt, types.ETDatetime, types.ETInt, types.ETDatetime}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETInt, types.ETDatetime, types.ETInt, types.ETDatetime, types.ETDatetime}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETInt, types.ETDuration}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETInt, types.ETDuration, types.ETDuration}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETInt, types.ETDuration, types.ETInt, types.ETDuration}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETInt, types.ETDuration, types.ETInt, types.ETDuration, types.ETDuration}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},

		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETInt, types.ETJson}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETInt, types.ETJson, types.ETJson}, geners: []dataGenerator{defaultControlIntGener}},
		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETInt, types.ETJson, types.ETInt, types.ETJson}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETInt, types.ETJson, types.ETInt, types.ETJson, types.ETJson}, geners: []dataGenerator{defaultControlIntGener, nil, defaultControlIntGener}},
	},

	ast.Ifnull: {

		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt}},

		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETReal, types.ETReal}},

		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETDecimal, types.ETDecimal}},

		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETString, types.ETString}},

		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETDatetime, types.ETDatetime}},

		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETDuration, types.ETDuration}},

		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETJson, types.ETJson}},
	},

	ast.If: {

		{retEvalType: types.ETInt, childrenTypes: []types.EvalType{types.ETInt, types.ETInt, types.ETInt}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETReal, childrenTypes: []types.EvalType{types.ETInt, types.ETReal, types.ETReal}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETDecimal, childrenTypes: []types.EvalType{types.ETInt, types.ETDecimal, types.ETDecimal}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETString, childrenTypes: []types.EvalType{types.ETInt, types.ETString, types.ETString}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETDatetime, childrenTypes: []types.EvalType{types.ETInt, types.ETDatetime, types.ETDatetime}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETDuration, childrenTypes: []types.EvalType{types.ETInt, types.ETDuration, types.ETDuration}, geners: []dataGenerator{defaultControlIntGener}},

		{retEvalType: types.ETJson, childrenTypes: []types.EvalType{types.ETInt, types.ETJson, types.ETJson}, geners: []dataGenerator{defaultControlIntGener}},
	},
}

func (s *testEvaluatorSuite) TestVectorizedBuiltinControlEvalOneVecGenerated(c *C) {
	testVectorizedEvalOneVec(c, vecBuiltinControlCases)
}

func (s *testEvaluatorSuite) TestVectorizedBuiltinControlFuncGenerated(c *C) {
	testVectorizedBuiltinFunc(c, vecBuiltinControlCases)
}

func BenchmarkVectorizedBuiltinControlEvalOneVecGenerated(b *testing.B) {
	benchmarkVectorizedEvalOneVec(b, vecBuiltinControlCases)
}

func BenchmarkVectorizedBuiltinControlFuncGenerated(b *testing.B) {
	benchmarkVectorizedBuiltinFunc(b, vecBuiltinControlCases)
}
