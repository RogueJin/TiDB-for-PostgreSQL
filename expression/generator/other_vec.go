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

// +build ignore

package main

import (
	"bytes"
	"go/format"
	"io/ioutil"
	"log"
	"path/filepath"
	"text/template"

	. "github.com/pingcap/tidb/expression/generator/helper"
)

const header = `// Copyright 2019 PingCAP, Inc.
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
`

const newLine = "\n"

const builtinOtherImports = `import (
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/types/json"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/collate"
)
`

var builtinInTmpl = template.Must(template.New("builtinInTmpl").Parse(`
{{ define "BufAllocator" }}
	buf0, err := b.bufAllocator.get(types.ET{{ .Input.ETName }}, n)
	if err != nil {
		return err
	}
	defer b.bufAllocator.put(buf0)
	if err := b.args[0].VecEval{{ .Input.TypeName }}(b.ctx, input, buf0); err != nil {
		return err
	}
	buf1, err := b.bufAllocator.get(types.ET{{ .Input.ETName }}, n)
	if err != nil {
		return err
	}
	defer b.bufAllocator.put(buf1)
{{ end }}
{{ define "SetHasNull" }}
	for i := 0; i < n; i++ {
		if result.IsNull(i) {
			result.SetNull(i, hasNull[i])
		}
	}
	return nil
{{ end }}
{{ define "Compare" }}
	{{ if eq .Input.TypeName "Int" -}}
		compareResult = 1
		switch {
			case (isUnsigned0 && isUnsigned), (!isUnsigned0 && !isUnsigned):
				if arg1 == arg0 {
					compareResult = 0
				}
			case !isUnsigned0 && isUnsigned:
				if arg0 >= 0 && arg1 == arg0 {
					compareResult = 0
				}
			case isUnsigned0 && !isUnsigned:
				if arg1 >= 0 && arg1 == arg0 {
					compareResult = 0
				}
		}
	{{- else if eq .Input.TypeName "Decimal" -}}
		compareResult = 1
		if arg0.Compare(&arg1) == 0 {
			compareResult = 0
		}
	{{- else if eq .Input.TypeName "Time" -}}
		compareResult = arg0.Compare(arg1)
	{{- else if eq .Input.TypeName "Duration" -}}
		compareResult = types.CompareDuration(arg0, arg1)
	{{- else if eq .Input.TypeName "JSON" -}}
		compareResult = json.CompareBinary(arg0, arg1)
	{{- else if eq .Input.TypeName "String" -}}
		compareResult = types.CompareString(arg0, arg1, b.collation)
	{{- else -}}
		compareResult = types.Compare{{ .Input.TypeNameInColumn }}(arg0, arg1)
	{{- end -}}
{{ end }}

{{ range . }}
{{ $InputInt := (eq .Input.TypeName "Int") }}
{{ $InputJSON := (eq .Input.TypeName "JSON")}}
{{ $InputString := (eq .Input.TypeName "String") }}
{{ $InputFixed := ( .Input.Fixed ) }}
{{ $UseHashKey := ( or (eq .Input.TypeName "Decimal") (eq .Input.TypeName "JSON") )}}
{{ $InputTime := (eq .Input.TypeName "Time") }}
func (b *{{.SigName}}) vecEvalInt(input *chunk.Chunk, result *chunk.Column) error {
	n := input.NumRows()
	{{- template "BufAllocator" . }}
	{{- if $InputFixed }}
		args0 := buf0.{{.Input.TypeNameInColumn}}s()
	{{- end }}
	result.ResizeInt64(n, true)
	r64s := result.Int64s()
	for i:=0; i<n; i++ {
		r64s[i] = 0
	}
	hasNull := make([]bool, n)
	{{- if not $InputJSON}}
	if b.hasNull {
		for i := 0; i < n; i++ {
			hasNull[i] = true
		}
	}
	{{- end }}
	{{- if $InputInt }}
		isUnsigned0 := mysql.HasUnsignedFlag(b.args[0].GetType().Flag)
	{{- end }}
	var compareResult int
	args := b.args
	{{- if not $InputJSON}}
	if len(b.hashSet) != 0 {
		{{- if $InputString }}
			collator := collate.GetCollator(b.collation)
		{{- end }}
		args = b.nonConstArgs
		for i := 0; i < n; i++ {
			if buf0.IsNull(i) {
				hasNull[i] = true
				continue
			}
			{{- if $InputInt }}
				arg0 := args0[i]
				if isUnsigned, ok := b.hashSet[arg0]; ok {
					if (isUnsigned0 && isUnsigned) || (!isUnsigned0 && !isUnsigned) {
						r64s[i] = 1
						result.SetNull(i, false)
					}
					if arg0 >= 0 {
						r64s[i] = 1
						result.SetNull(i, false)
					}
				}
			{{- else }}
				{{- if $InputFixed }}
					arg0 := args0[i]
				{{- else }}
					arg0 := buf0.Get{{ .Input.TypeName }}(i)
				{{- end }}

				{{- if $UseHashKey }}
					key, err := arg0.ToHashKey()
					if err != nil{
						return err
					}
					if _, ok := b.hashSet[string(key)]; ok {
						r64s[i] = 1
						result.SetNull(i, false)
					}
				{{- else if $InputString }}
					if _, ok := b.hashSet[string(collator.Key(arg0))]; ok {
						r64s[i] = 1
						result.SetNull(i, false)
					}
				{{- else if $InputTime }}
					if _, ok := b.hashSet[arg0.CoreTime()]; ok {
						r64s[i] = 1
						result.SetNull(i, false)
					}
				{{- else }}
					if _, ok := b.hashSet[arg0]; ok {
						r64s[i] = 1
						result.SetNull(i, false)
					}
				{{- end }}
			{{- end }}
		}
	}
	{{- end }}

	for j := 1; j < len(args); j++ {
		if err := args[j].VecEval{{ .Input.TypeName }}(b.ctx, input, buf1); err != nil {
			return err
		}
		{{- if $InputInt }}
			isUnsigned := mysql.HasUnsignedFlag(args[j].GetType().Flag)
		{{- end }}
		{{- if $InputFixed }}
			args1 := buf1.{{.Input.TypeNameInColumn}}s()
			buf1.MergeNulls(buf0)
		{{- end }}
		for i := 0; i < n; i++ {
			if r64s[i] != 0 {
				continue
			}
{{- /* if is null */}}
			if buf1.IsNull(i) {{- if not $InputFixed -}} || buf0.IsNull(i) {{- end -}} {
				hasNull[i] = true
				continue
			}

{{- /* get args */}}
			{{- if $InputFixed }}
				arg0 := args0[i]
				arg1 := args1[i]
			{{- else }}
				arg0 := buf0.Get{{ .Input.TypeName }}(i)
				arg1 := buf1.Get{{ .Input.TypeName }}(i)
			{{- end }}

{{- /* compare */}}
			{{- template "Compare" . }}
			if compareResult == 0 {
				result.SetNull(i, false)
				r64s[i] = 1
			}
		} // for i
	} // for j
	{{- template "SetHasNull" . -}}
}

func (b *{{.SigName}}) vectorized() bool {
	return true
}
{{ end }}{{/* range */}}
`))

var testFile = template.Must(template.New("").Parse(`// Copyright 2019 PingCAP, Inc.
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
	"fmt"
	"math/rand"
	"testing"
	"time"

	. "github.com/pingcap/check"
	"github.com/DigitalChinaOpenSource/DCParser/ast"
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/types/json"
)

type inGener struct {
	defaultGener
}

func (g inGener) gen() interface{} {
	if rand.Float64() < g.nullRation {
		return nil
	}
	randNum := rand.Int63n(10)
	switch g.eType {
	case types.ETInt:
		if rand.Float64() < 0.5 {
			return -randNum
		}
		return randNum
	case types.ETReal:
		if rand.Float64() < 0.5 {
			return -float64(randNum)
		}
		return float64(randNum)
	case types.ETDecimal:
		d := new(types.MyDecimal)
		f := float64(randNum * 100000)
		if err := d.FromFloat64(f); err != nil {
			panic(err)
		}
		return d
	case types.ETDatetime, types.ETTimestamp:
		gt := types.FromDate(2019, 11, 2, 22, 00, int(randNum), rand.Intn(1000000))
		t := types.NewTime(gt, convertETType(g.eType), 0)
		return t
	case types.ETDuration:
		return types.Duration{ Duration: time.Duration(randNum) }
	case types.ETJson:
		j := new(json.BinaryJSON)
		jsonStr := fmt.Sprintf("{\"key\":%v}", randNum)
		if err := j.UnmarshalJSON([]byte(jsonStr)); err != nil {
			panic(err)
		}
		return *j
	case types.ETString:
		return fmt.Sprint(randNum)
	}
	return randNum
}

{{/* Add more test cases here if we have more functions in this file */}}
var vecBuiltin{{ .Category }}GeneratedCases = map[string][]vecExprBenchCase {
{{- range $.Functions }}
	ast.{{ .FuncName }}: {
	{{- range .Sigs }}
		// {{ .SigName }}
		{
			retEvalType: types.ET{{ .Output.ETName }},
			childrenTypes: []types.EvalType{
				types.ET{{ .Input.ETName }},
				types.ET{{ .Input.ETName }},
				types.ET{{ .Input.ETName }},
				types.ET{{ .Input.ETName }},
			},
			geners: []dataGenerator{
				inGener{*newDefaultGener(0.2, types.ET{{.Input.ETName}})},
				inGener{*newDefaultGener(0.2, types.ET{{.Input.ETName}})},
				inGener{*newDefaultGener(0.2, types.ET{{.Input.ETName}})},
				inGener{*newDefaultGener(0.2, types.ET{{.Input.ETName}})},
			},
		},
	{{- end }}
	{{- range .Sigs }}
		// {{ .SigName }} with const arguments
		{
			retEvalType: types.ET{{ .Output.ETName }},
			childrenTypes: []types.EvalType{
				types.ET{{ .Input.ETName }},
				types.ET{{ .Input.ETName }}, types.ET{{ .Input.ETName }},
			},
			constants: []*Constant{
				nil,
				{{- if eq .Input.ETName "Int" }}
					{Value: types.NewDatum(1), RetType: types.NewFieldType(mysql.TypeInt24)},
					{Value: types.NewDatum(2), RetType: types.NewFieldType(mysql.TypeInt24)},
				{{- end }}
				{{- if eq .Input.ETName "String" }}
					{Value: types.NewStringDatum("aaaa"), RetType: types.NewFieldType(mysql.TypeString)},
					{Value: types.NewStringDatum("bbbb"), RetType: types.NewFieldType(mysql.TypeString)},
				{{- end }}
				{{- if eq .Input.ETName "Datetime" }}
					{Value: types.NewTimeDatum(dateTimeFromString("2019-01-01")), RetType: types.NewFieldType(mysql.TypeDatetime)},
					{Value: types.NewTimeDatum(dateTimeFromString("2019-01-01")), RetType: types.NewFieldType(mysql.TypeDatetime)},
				{{- end }}
				{{- if eq .Input.ETName "Json" }}
					{Value: types.NewJSONDatum(json.CreateBinary("aaaa")), RetType: types.NewFieldType(mysql.TypeJSON)},
					{Value: types.NewJSONDatum(json.CreateBinary("bbbb")), RetType: types.NewFieldType(mysql.TypeJSON)},
				{{- end }}
				{{- if eq .Input.ETName "Duration" }}
					{Value: types.NewDurationDatum(types.Duration{Duration: time.Duration(1000)}), RetType: types.NewFieldType(mysql.TypeDuration)},
					{Value: types.NewDurationDatum(types.Duration{Duration: time.Duration(2000)}), RetType: types.NewFieldType(mysql.TypeDuration)},
				{{- end }}
				{{- if eq .Input.ETName "Real" }}
					{Value: types.NewFloat64Datum(0.1), RetType: types.NewFieldType(mysql.TypeFloat)},
					{Value: types.NewFloat64Datum(0.2), RetType: types.NewFieldType(mysql.TypeFloat)},
				{{- end }}
				{{- if eq .Input.ETName "Decimal" }}
					{Value: types.NewDecimalDatum(types.NewDecFromInt(10)), RetType: types.NewFieldType(mysql.TypeDecimal)},
					{Value: types.NewDecimalDatum(types.NewDecFromInt(20)), RetType: types.NewFieldType(mysql.TypeDecimal)},
				{{- end }}
			},
		},
	{{- end }}
{{- end }}
	},
}

func (s *testEvaluatorSuite) TestVectorizedBuiltin{{.Category}}EvalOneVecGenerated(c *C) {
	testVectorizedEvalOneVec(c, vecBuiltin{{.Category}}GeneratedCases)
}

func (s *testEvaluatorSuite) TestVectorizedBuiltin{{.Category}}FuncGenerated(c *C) {
	testVectorizedBuiltinFunc(c, vecBuiltin{{.Category}}GeneratedCases)
}

func BenchmarkVectorizedBuiltin{{.Category}}EvalOneVecGenerated(b *testing.B) {
	benchmarkVectorizedEvalOneVec(b, vecBuiltin{{.Category}}GeneratedCases)
}

func BenchmarkVectorizedBuiltin{{.Category}}FuncGenerated(b *testing.B) {
	benchmarkVectorizedBuiltinFunc(b, vecBuiltin{{.Category}}GeneratedCases)
}
`))

type sig struct {
	SigName       string
	Input, Output TypeContext
}

var inSigsTmpl = []sig{
	{SigName: "builtinInIntSig", Input: TypeInt, Output: TypeInt},
	{SigName: "builtinInStringSig", Input: TypeString, Output: TypeInt},
	{SigName: "builtinInDecimalSig", Input: TypeDecimal, Output: TypeInt},
	{SigName: "builtinInRealSig", Input: TypeReal, Output: TypeInt},
	{SigName: "builtinInTimeSig", Input: TypeDatetime, Output: TypeInt},
	{SigName: "builtinInDurationSig", Input: TypeDuration, Output: TypeInt},
	{SigName: "builtinInJSONSig", Input: TypeJSON, Output: TypeInt},
}

type function struct {
	FuncName string
	Sigs     []sig
}

var tmplVal = struct {
	Category  string
	Functions []function
}{
	Category: "Other",
	Functions: []function{
		{FuncName: "In", Sigs: inSigsTmpl},
	},
}

func generateDotGo(fileName string) error {
	w := new(bytes.Buffer)
	w.WriteString(header)
	w.WriteString(newLine)
	w.WriteString(builtinOtherImports)
	err := builtinInTmpl.Execute(w, inSigsTmpl)
	if err != nil {
		return err
	}
	data, err := format.Source(w.Bytes())
	if err != nil {
		log.Println("[Warn]", fileName+": gofmt failed", err)
		data = w.Bytes() // write original data for debugging
	}
	return ioutil.WriteFile(fileName, data, 0644)
}

func generateTestDotGo(fileName string) error {
	w := new(bytes.Buffer)
	err := testFile.Execute(w, tmplVal)
	if err != nil {
		return err
	}
	data, err := format.Source(w.Bytes())
	if err != nil {
		log.Println("[Warn]", fileName+": gofmt failed", err)
		data = w.Bytes() // write original data for debugging
	}
	return ioutil.WriteFile(fileName, data, 0644)
}

// generateOneFile generate one xxx.go file and the associated xxx_test.go file.
func generateOneFile(fileNamePrefix string) (err error) {
	err = generateDotGo(fileNamePrefix + ".go")
	if err != nil {
		return
	}
	err = generateTestDotGo(fileNamePrefix + "_test.go")
	return
}

func main() {
	var err error
	outputDir := "."
	err = generateOneFile(filepath.Join(outputDir, "builtin_other_vec_generated"))
	if err != nil {
		log.Fatalln("generateOneFile", err)
	}
}
