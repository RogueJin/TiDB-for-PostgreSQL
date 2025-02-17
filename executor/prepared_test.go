// Copyright 2016 PingCAP, Inc.
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

package executor_test

import (
	"crypto/tls"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/DigitalChinaOpenSource/DCParser/auth"
	"github.com/DigitalChinaOpenSource/DCParser/model"
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	plannercore "github.com/pingcap/tidb/planner/core"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/testkit"
	"github.com/pingcap/tidb/util/testleak"
)

func (s *testSuite1) TestPreparedNameResolver(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t (id int, KEY id (id))")
	tk.MustExec("prepare stmt from 'select * from t limit ? offset ?'")
	_, err := tk.Exec("prepare stmt from 'select b from t'")
	c.Assert(err.Error(), Equals, "[planner:1054]Unknown column 'b' in 'field list'")

	_, err = tk.Exec("prepare stmt from '(select * FROM t) union all (select * FROM t) order by a limit ?'")
	c.Assert(err.Error(), Equals, "[planner:1054]Unknown column 'a' in 'order clause'")
}

// a 'create table' DDL statement should be accepted if it has no parameters.
func (s *testSuite1) TestPreparedDDL(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")
	tk.MustExec("prepare stmt from 'create table t (id int, KEY id (id))'")
}

// TestUnsupportedStmtForPrepare is related to https://github.com/pingcap/tidb/issues/17412
func (s *testSuite1) TestUnsupportedStmtForPrepare(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec(`prepare stmt0 from "create table t0(a int primary key)"`)
	tk.MustGetErrCode(`prepare stmt1 from "execute stmt0"`, mysql.ErrUnsupportedPs)
	tk.MustGetErrCode(`prepare stmt2 from "deallocate prepare stmt0"`, mysql.ErrUnsupportedPs)
	tk.MustGetErrCode(`prepare stmt4 from "prepare stmt3 from 'create table t1(a int, b int)'"`, mysql.ErrUnsupportedPs)
}

func (s *testSuite1) TestIgnorePlanCache(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")

	tk.MustExec("create table t (id int primary key, num int)")
	tk.MustExec("insert into t values (1, 1)")
	tk.MustExec("insert into t values (2, 2)")
	tk.MustExec("insert into t values (3, 3)")
	tk.MustExec("prepare stmt from 'select /*+ IGNORE_PLAN_CACHE() */ * from t where id=?'")
	tk.MustExec("set @ignore_plan_doma = 1")
	tk.MustExec("execute stmt using @ignore_plan_doma")
	c.Assert(tk.Se.GetSessionVars().StmtCtx.UseCache, IsFalse)
}

func (s *testSuite9) TestPrepareStmtAfterIsolationReadChange(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.Se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost", CurrentUser: true, AuthUsername: "root", AuthHostname: "%"}, nil, []byte("012345678901234567890"))

	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t(a int)")
	// create virtual tiflash replica.
	dom := domain.GetDomain(tk.Se)
	is := dom.InfoSchema()
	db, exists := is.SchemaByName(model.NewCIStr("test"))
	c.Assert(exists, IsTrue)
	for _, tblInfo := range db.Tables {
		if tblInfo.Name.L == "t" {
			tblInfo.TiFlashReplica = &model.TiFlashReplicaInfo{
				Count:     1,
				Available: true,
			}
		}
	}

	tk.MustExec("set @@session.tidb_isolation_read_engines='tikv'")
	tk.MustExec("set @@tidb_enable_collect_execution_info=0;")
	tk.MustExec("prepare stmt from \"select * from t\"")
	tk.MustQuery("execute stmt")
	tkProcess := tk.Se.ShowProcess()
	ps := []*util.ProcessInfo{tkProcess}
	tk.Se.SetSessionManager(&mockSessionManager1{PS: ps})
	rows := tk.MustQuery(fmt.Sprintf("explain for connection %d", tkProcess.ID)).Rows()
	c.Assert(rows[len(rows)-1][2], Equals, "cop[tikv]")

	tk.MustExec("set @@session.tidb_isolation_read_engines='tiflash'")
	tk.MustExec("execute stmt")
	tkProcess = tk.Se.ShowProcess()
	ps = []*util.ProcessInfo{tkProcess}
	tk.Se.SetSessionManager(&mockSessionManager1{PS: ps})
	rows = tk.MustQuery(fmt.Sprintf("explain for connection %d", tkProcess.ID)).Rows()
	c.Assert(rows[len(rows)-1][2], Equals, "cop[tiflash]")

	c.Assert(len(tk.Se.GetSessionVars().PreparedStmts), Equals, 1)
	c.Assert(tk.Se.GetSessionVars().PreparedStmts[1].(*plannercore.CachedPrepareStmt).NormalizedSQL, Equals, "select * from t")
	c.Assert(tk.Se.GetSessionVars().PreparedStmts[1].(*plannercore.CachedPrepareStmt).NormalizedPlan, Equals, "")
}

type mockSessionManager2 struct {
	se     session.Session
	killed bool
}

func (sm *mockSessionManager2) ShowProcessList() map[uint64]*util.ProcessInfo {
	pl := make(map[uint64]*util.ProcessInfo)
	if pi, ok := sm.GetProcessInfo(0); ok {
		pl[pi.ID] = pi
	}
	return pl
}

func (sm *mockSessionManager2) GetProcessInfo(id uint64) (pi *util.ProcessInfo, notNil bool) {
	pi = sm.se.ShowProcess()
	if pi != nil {
		notNil = true
	}
	return
}
func (sm *mockSessionManager2) Kill(connectionID uint64, query bool) {
	sm.killed = true
	atomic.StoreUint32(&sm.se.GetSessionVars().Killed, 1)
}
func (sm *mockSessionManager2) UpdateTLSConfig(cfg *tls.Config) {}

var _ = SerialSuites(&testSuite12{&baseTestSuite{}})

type testSuite12 struct {
	*baseTestSuite
}

func (s *testSuite12) TestPreparedStmtWithHint(c *C) {
	// see https://github.com/pingcap/tidb/issues/18535
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	defer func() {
		store.Close()
		dom.Close()
	}()

	se, err := session.CreateSession4Test(store)
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	tk.Se = se

	sm := &mockSessionManager2{
		se: se,
	}
	se.SetSessionManager(sm)
	go dom.ExpensiveQueryHandle().SetSessionManager(sm).Run()
	tk.MustExec("prepare stmt from \"select /*+ max_execution_time(100) */ sleep(10)\"")
	tk.MustQuery("execute stmt").Check(testkit.Rows("1"))
	c.Check(sm.killed, Equals, true)
}

func (s *testSuite9) TestPlanCacheOnPointGet(c *C) {
	defer testleak.AfterTest(c)()
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	defer func() {
		dom.Close()
		store.Close()
	}()
	orgEnable := plannercore.PreparedPlanCacheEnabled()
	defer func() {
		plannercore.SetPreparedPlanCache(orgEnable)
	}()
	plannercore.SetPreparedPlanCache(true)
	tk.MustExec("use test")

	// For point get
	tk.MustExec("drop table if exists t1")
	tk.MustExec("set @@tidb_enable_collect_execution_info=0;")
	tk.MustExec("create table t1(a varchar(20), b varchar(20), c varchar(20), primary key(a, b))")
	tk.MustExec("insert into t1 values('1','1','111'),('2','2','222'),('3','3','333')")
	tk.MustExec(`prepare stmt2 from "select * from t1 where t1.a = ? and t1.b = ?"`)
	tk.MustExec("set @v1 = '1'")
	tk.MustExec("set @v2 = '1'")
	tk.MustQuery("execute stmt2 using @v1,@v2").Check(testkit.Rows("1 1 111"))
	tk.MustQuery("select @@last_plan_from_cache").Check(testkit.Rows("0"))
	tk.MustExec("set @v1 = '2'")
	tk.MustExec("set @v2 = '2'")
	tk.MustQuery("execute stmt2 using @v1,@v2").Check(testkit.Rows("2 2 222"))
	tk.MustQuery("select @@last_plan_from_cache").Check(testkit.Rows("1"))
	tk.MustExec("set @v1 = '3'")
	tk.MustExec("set @v2 = '3'")
	tk.MustQuery("execute stmt2 using @v1,@v2").Check(testkit.Rows("3 3 333"))
	tkProcess := tk.Se.ShowProcess()
	ps := []*util.ProcessInfo{tkProcess}
	tk.Se.SetSessionManager(&mockSessionManager1{PS: ps})
	rows := tk.MustQuery(fmt.Sprintf("explain for connection %d", tkProcess.ID)).Rows()
	c.Assert(strings.Index(rows[len(rows)-1][0].(string), `Point_Get`), Equals, 0)

	// For CBO point get and batch point get
	// case 1:
	tk.MustExec(`drop table if exists ta, tb`)
	tk.MustExec(`create table ta (a int primary key, b int)`)
	tk.MustExec(`insert ta values (1, 1), (2, 2)`)
	tk.MustExec(`create table tb (a int primary key, b int)`)
	tk.MustExec(`insert tb values (1, 1), (2, 2)`)
	tk.MustExec(`prepare stmt1 from "select * from ta, tb where ta.a = tb.a and ta.a = ?"`)
	tk.MustExec(`set @v1 = 1, @v2 = 2`)
	tk.MustQuery(`execute stmt1 using @v1`).Check(testkit.Rows("1 1 1 1"))
	tk.MustQuery(`execute stmt1 using @v2`).Check(testkit.Rows("2 2 2 2"))
	tk.MustQuery("select @@last_plan_from_cache").Check(testkit.Rows("1"))

	// case 2:
	tk.MustExec(`drop table if exists ta, tb`)
	tk.MustExec(`create table ta (a varchar(10) primary key, b int not null)`)
	tk.MustExec(`insert ta values ('a', 1), ('b', 2)`)
	tk.MustExec(`create table tb (b int primary key, c int)`)
	tk.MustExec(`insert tb values (1, 1), (2, 2)`)
	tk.MustExec(`prepare stmt1 from "select * from ta, tb where ta.b = tb.b and ta.a = ?"`)
	tk.MustExec(`set @v1 = 'a', @v2 = 'b'`)
	tk.MustQuery(`execute stmt1 using @v1`).Check(testkit.Rows("a 1 1 1"))
	tk.MustQuery(`execute stmt1 using @v2`).Check(testkit.Rows("b 2 2 2"))
	tk.MustQuery("select @@last_plan_from_cache").Check(testkit.Rows("1"))
	tk.MustQuery(`execute stmt1 using @v2`).Check(testkit.Rows("b 2 2 2"))
	tkProcess = tk.Se.ShowProcess()
	ps = []*util.ProcessInfo{tkProcess}
	tk.Se.SetSessionManager(&mockSessionManager1{PS: ps})
	rows = tk.MustQuery(fmt.Sprintf("explain for connection %d", tkProcess.ID)).Rows()
	c.Assert(strings.Index(rows[1][0].(string), `Point_Get`), Equals, 6)

	// case 3:
	tk.MustExec(`drop table if exists ta, tb`)
	tk.MustExec(`create table ta (a varchar(10), b varchar(10), c int, primary key (a, b))`)
	tk.MustExec(`insert ta values ('a', 'a', 1), ('b', 'b', 2), ('c', 'c', 3)`)
	tk.MustExec(`create table tb (b int primary key, c int)`)
	tk.MustExec(`insert tb values (1, 1), (2, 2), (3,3)`)
	tk.MustExec(`prepare stmt1 from "select * from ta, tb where ta.c = tb.b and ta.a = ? and ta.b = ?"`)
	tk.MustExec(`set @v1 = 'a', @v2 = 'b', @v3 = 'c'`)
	tk.MustQuery(`execute stmt1 using @v1, @v1`).Check(testkit.Rows("a a 1 1 1"))
	tk.MustQuery(`execute stmt1 using @v2, @v2`).Check(testkit.Rows("b b 2 2 2"))
	tk.MustExec(`prepare stmt2 from "select * from ta, tb where ta.c = tb.b and (ta.a, ta.b) in ((?, ?), (?, ?))"`)
	tk.MustQuery(`execute stmt2 using @v1, @v1, @v2, @v2`).Check(testkit.Rows("a a 1 1 1", "b b 2 2 2"))
	tk.MustQuery(`execute stmt2 using @v2, @v2, @v3, @v3`).Check(testkit.Rows("b b 2 2 2", "c c 3 3 3"))
}

func (s *testPrepareSuite) TestPlanCacheWithDifferentVariableTypes(c *C) {
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	defer func() {
		dom.Close()
		store.Close()
	}()
	orgEnable := plannercore.PreparedPlanCacheEnabled()
	defer func() {
		plannercore.SetPreparedPlanCache(orgEnable)
	}()
	plannercore.SetPreparedPlanCache(true)
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t1, t2")
	tk.MustExec("set @@tidb_enable_collect_execution_info=0;")
	tk.MustExec("create table t1(a varchar(20), b int, c float, key(b, a))")
	tk.MustExec("insert into t1 values('1',1,1.1),('2',2,222),('3',3,333)")
	tk.MustExec("create table t2(a varchar(20), b int, c float, key(b, a))")
	tk.MustExec("insert into t2 values('3',3,3.3),('2',2,222),('3',3,333)")

	var input []struct {
		PrepareStmt string
		Executes    []struct {
			Vars []struct {
				Name  string
				Value string
			}
			ExecuteSQL string
		}
	}
	var output []struct {
		PrepareStmt string
		Executes    []struct {
			SQL  string
			Vars []struct {
				Name  string
				Value string
			}
			Plan             []string
			LastPlanUseCache string
			Result           []string
		}
	}
	s.testData.GetTestCases(c, &input, &output)
	for i, tt := range input {
		tk.MustExec(tt.PrepareStmt)
		s.testData.OnRecord(func() {
			output[i].PrepareStmt = tt.PrepareStmt
			output[i].Executes = make([]struct {
				SQL  string
				Vars []struct {
					Name  string
					Value string
				}
				Plan             []string
				LastPlanUseCache string
				Result           []string
			}, len(tt.Executes))
		})
		c.Assert(output[i].PrepareStmt, Equals, tt.PrepareStmt)
		for j, exec := range tt.Executes {
			for _, v := range exec.Vars {
				tk.MustExec(fmt.Sprintf(`set @%s = %s`, v.Name, v.Value))
			}
			res := tk.MustQuery(exec.ExecuteSQL)
			lastPlanUseCache := tk.MustQuery("select @@last_plan_from_cache").Rows()[0][0]
			tk.MustQuery(exec.ExecuteSQL)
			tkProcess := tk.Se.ShowProcess()
			ps := []*util.ProcessInfo{tkProcess}
			tk.Se.SetSessionManager(&mockSessionManager1{PS: ps})
			plan := tk.MustQuery(fmt.Sprintf("explain for connection %d", tkProcess.ID))
			s.testData.OnRecord(func() {
				output[i].Executes[j].SQL = exec.ExecuteSQL
				output[i].Executes[j].Plan = s.testData.ConvertRowsToStrings(plan.Rows())
				output[i].Executes[j].Vars = exec.Vars
				output[i].Executes[j].LastPlanUseCache = lastPlanUseCache.(string)
				output[i].Executes[j].Result = s.testData.ConvertRowsToStrings(res.Rows())
			})
			c.Assert(output[i].Executes[j].SQL, Equals, exec.ExecuteSQL)
			plan.Check(testkit.Rows(output[i].Executes[j].Plan...))
			c.Assert(output[i].Executes[j].Vars, DeepEquals, exec.Vars)
			c.Assert(output[i].Executes[j].LastPlanUseCache, Equals, lastPlanUseCache.(string))
			res.Check(testkit.Rows(output[i].Executes[j].Result...))
		}
	}
}

type testParamType struct {
	sql        string
	expectType []byte
	paramCount int
}

//
func (s *testPrepareSuite) TestGetPrepareParamType(c *C) {
	store, _, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)

	se, err := session.CreateSession4Test(store)
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	tk.Se = se

	tk.Se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost", CurrentUser: true, AuthUsername: "root", AuthHostname: "%"}, nil, []byte("012345678901234567890"))

	sm := &mockSessionManager2{
		se: se,
	}
	se.SetSessionManager(sm)

	tk.MustExec("use test")
	tk.MustExec("drop table if exists student")

	tk.MustExec("create table student (id int primary key, name varchar(255), age int)")
	tk.MustExec("insert into student values(1, \"zhangsan\", 18)")

	sqlList := []*testParamType{
		{
			// limit and offset followed by parameters are constants,
			// their kind be set to "0" by default.
			sql:        "select * from student where name = $1 limit $2 offset 1",
			paramCount: 2,
			expectType: []byte{mysql.TypeVarchar, mysql.TypeDecimal},
		}, {
			// If limit and offset followed by parameters are ParamMakerExpr,
			// TiDB-Server will generate a LogicalTableDual,
			// We can't get any param type.
			// todo get param type in this case.
			sql:        "select * from student where name = $1 limit $2 offset $3",
			paramCount: 3,
			expectType: []byte{mysql.TypeDecimal, mysql.TypeDecimal, mysql.TypeDecimal},
		},
	}

	for _, v1 := range sqlList {
		stmtID, paramCount, _, err := tk.Se.PrepareStmt(v1.sql, "")
		c.Assert(paramCount, Equals, v1.paramCount)
		c.Assert(err, IsNil)

		if cachedStmt, ok := tk.Se.GetSessionVars().PreparedStmts[stmtID].(*plannercore.CachedPrepareStmt); ok {
			cachedParams := cachedStmt.PreparedAst.Params
			for i, _ := range cachedParams {
				paramType := cachedParams[i].GetType().Tp
				c.Assert(paramType, Equals, v1.expectType[i])
			}
		}
	}
}
