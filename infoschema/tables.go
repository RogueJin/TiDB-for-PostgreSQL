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

package infoschema

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/DigitalChinaOpenSource/DCParser/charset"
	"github.com/DigitalChinaOpenSource/DCParser/model"
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/DigitalChinaOpenSource/DCParser/terror"
	"github.com/pingcap/errors"
	"github.com/pingcap/failpoint"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/domain/infosync"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/meta/autoid"
	"github.com/pingcap/tidb/sessionctx"
	"github.com/pingcap/tidb/sessionctx/variable"
	"github.com/pingcap/tidb/store/helper"
	"github.com/pingcap/tidb/store/tikv"
	"github.com/pingcap/tidb/table"
	"github.com/pingcap/tidb/types"
	binaryJson "github.com/pingcap/tidb/types/json"
	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/execdetails"
	"github.com/pingcap/tidb/util/pdapi"
)

// todo Table names in system tables need to be replaced with lower case
const (
	// TableSchemata is the string constant of infoschema table.
	TableSchemata = "TIDB_SCHEMATA"
	// TableTables is the string constant of infoschema table.
	TableTables = "TIDB_TABLES"
	// TableColumns is the string constant of infoschema table
	TableColumns          = "TIDB_COLUMNS"
	tableColumnStatistics = "COLUMN_STATISTICS"
	// TableStatistics is the string constant of infoschema table
	TableStatistics = "STATISTICS"
	// TableCharacterSets is the string constant of infoschema charactersets memory table
	TableCharacterSets = "TiDB_CHARACTER_SETS"
	// TableCollations is the string constant of infoschema collations memory table.
	TableCollations = "TIDB_COLLATIONS"
	tableFiles      = "FILES"
	// CatalogVal is the string constant of TABLE_CATALOG.
	CatalogVal = "def"
	// TableProfiling is the string constant of infoschema table.
	TableProfiling = "PROFILING"
	// TablePartitions is the string constant of infoschema table.
	TablePartitions = "PARTITIONS"
	// TableKeyColumn is the string constant of TiDB_KEY_COLUMN_USAGE.
	TableKeyColumn  = "TiDB_KEY_COLUMN_USAGE"
	tableReferConst = "REFERENTIAL_CONSTRAINTS"
	// TableSessionVar is the string constant of SESSION_VARIABLES.
	TableSessionVar = "SESSION_VARIABLES"
	tablePlugins    = "PLUGINS"
	// TableConstraints is the string constant of TABLE_CONSTRAINTS.
	TableConstraints = "TIDB_TABLE_CONSTRAINTS"
	tableTriggers    = "TRIGGERS"
	// TableUserPrivileges is the string constant of infoschema user privilege table.
	TableUserPrivileges   = "USER_PRIVILEGES"
	tableSchemaPrivileges = "SCHEMA_PRIVILEGES"
	tableTablePrivileges  = "TIDB_TABLE_PRIVILEGES"
	tableColumnPrivileges = "COLUMN_PRIVILEGES"
	// TableEngines is the string constant of infoschema table.
	TableEngines = "ENGINES"
	// TableViews is the string constant of infoschema table.
	TableViews           = "TIDB_VIEWS"
	tableRoutines        = "TIDB_ROUTINES"
	tableParameters      = "PARAMETERS"
	tableEvents          = "EVENTS"
	tableGlobalStatus    = "GLOBAL_STATUS"
	tableGlobalVariables = "GLOBAL_VARIABLES"
	tableSessionStatus   = "SESSION_STATUS"
	tableOptimizerTrace  = "OPTIMIZER_TRACE"
	tableTableSpaces     = "TABLESPACES"
	// TableCollationCharacterSetApplicability is the string constant of infoschema memory table.
	TableCollationCharacterSetApplicability = "TiDB_COLLATION_CHARACTER_SET_APPLICABILITY"
	// TableProcesslist is the string constant of infoschema table.
	TableProcesslist = "PROCESSLIST"
	// TableTiDBIndexes is the string constant of infoschema table
	TableTiDBIndexes = "TIDB_INDEXES"
	// TableTiDBHotRegions is the string constant of infoschema table
	TableTiDBHotRegions  = "TIDB_HOT_REGIONS"
	tableTiKVStoreStatus = "TIKV_STORE_STATUS"
	// TableAnalyzeStatus is the string constant of Analyze Status
	TableAnalyzeStatus    = "ANALYZE_STATUS"
	tableTiKVRegionStatus = "TIKV_REGION_STATUS"
	// TableTiKVRegionPeers is the string constant of infoschema table
	TableTiKVRegionPeers = "TIKV_REGION_PEERS"
	// TableTiDBServersInfo is the string constant of TiDB server information table.
	TableTiDBServersInfo = "TIDB_SERVERS_INFO"
	// TableSlowQuery is the string constant of slow query memory table.
	TableSlowQuery = "SLOW_QUERY"
	// TableClusterInfo is the string constant of cluster info memory table.
	TableClusterInfo = "CLUSTER_INFO"
	// TableClusterConfig is the string constant of cluster configuration memory table.
	TableClusterConfig = "CLUSTER_CONFIG"
	// TableClusterLog is the string constant of cluster log memory table.
	TableClusterLog = "CLUSTER_LOG"
	// TableClusterLoad is the string constant of cluster load memory table.
	TableClusterLoad = "CLUSTER_LOAD"
	// TableClusterHardware is the string constant of cluster hardware table.
	TableClusterHardware = "CLUSTER_HARDWARE"
	// TableClusterSystemInfo is the string constant of cluster system info table.
	TableClusterSystemInfo = "CLUSTER_SYSTEMINFO"
	// TableTiFlashReplica is the string constant of tiflash replica table.
	TableTiFlashReplica = "TIFLASH_REPLICA"
	// TableInspectionResult is the string constant of inspection result table.
	TableInspectionResult = "INSPECTION_RESULT"
	// TableMetricTables is a table that contains all metrics table definition.
	TableMetricTables = "METRICS_TABLES"
	// TableMetricSummary is a summary table that contains all metrics.
	TableMetricSummary = "METRICS_SUMMARY"
	// TableMetricSummaryByLabel is a metric table that contains all metrics that group by label info.
	TableMetricSummaryByLabel = "METRICS_SUMMARY_BY_LABEL"
	// TableInspectionSummary is the string constant of inspection summary table.
	TableInspectionSummary = "INSPECTION_SUMMARY"
	// TableInspectionRules is the string constant of currently implemented inspection and summary rules.
	TableInspectionRules = "INSPECTION_RULES"
	// TableDDLJobs is the string constant of DDL job table.
	TableDDLJobs = "DDL_JOBS"
	// TableSequences is the string constant of all sequences created by user.
	TableSequences = "TIDB_SEQUENCES"
	// TableStatementsSummary is the string constant of statement summary table.
	TableStatementsSummary = "STATEMENTS_SUMMARY"
	// TableStatementsSummaryHistory is the string constant of statements summary history table.
	TableStatementsSummaryHistory = "STATEMENTS_SUMMARY_HISTORY"
	// TableTiFlashTables is the string constant of tiflash tables table.
	TableTiFlashTables = "TIFLASH_TABLES"
	// TableTiFlashSegments is the string constant of tiflash segments table.
	TableTiFlashSegments = "TIFLASH_SEGMENTS"
	// TableStorageStats is a table that contains all tables disk usage
	TableStorageStats = "TABLE_STORAGE_STATS"
	// TablePgInformationsSchemaCatalogName is a table that always contains one row and one column containing the name of the current database (current catalog, in SQL terminology).
	// https://www.postgresql.org/docs/13/infoschema-information-schema-catalog-name.html
	TablePgInformationsSchemaCatalogName = "information_schema_catalog_name"
	// TablePgAdministrableRoleAuthorizations identifies all roles that the current user has the admin option for.
	//https://www.postgresql.org/docs/13/infoschema-administrable-role-authorizations.html
	TablePgAdministrableRoleAuthorizations = "administrable_role_authorizations"
	// TablePgApplicableRole identifies all roles whose privileges the current user can use.
	//https://www.postgresql.org/docs/13/infoschema-applicable-roles.html
	TablePgApplicableRole = "applicable_roles"
	// TablePgAttributes contains information about the attributes of composite data types defined in the database.
	// https://www.postgresql.org/docs/13/infoschema-attributes.html
	TablePgAttributes = "attributes"
	// TablePgCharacterSets identifies the character sets available in the current database.
	// https://www.postgresql.org/docs/13/infoschema-character-sets.html
	TablePgCharacterSets = "character_sets"
	// TablePgCheckConstraintRoutineUsage identifies routines (functions and procedures) that are used by a check constraint.
	// https://www.postgresql.org/docs/13/infoschema-check-constraint-routine-usage.html
	TablePgCheckConstraintRoutineUsage = "check_constraint_routine_usage"
	// TablePgCheckConstraints contains all check constraints, either defined on a table or on a domain, that are owned by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-check-constraints.html
	TablePgCheckConstraints = "check_constraints"
	// TablePgCollations contains the collations available in the current database.
	// https://www.postgresql.org/docs/13/infoschema-collations.html
	TablePgCollations = "collations"
	// TablePgCollationCharacterSetApplicability  identifies which character set the available collations are applicable to.
	// https://www.postgresql.org/docs/13/infoschema-collation-character-set-applicab.html
	TablePgCollationCharacterSetApplicability = "collation_character_set_applicability"
	// TablePgColumnColumnUsage identifies all generated columns that depend on another base column in the same table.
	// https://www.postgresql.org/docs/13/infoschema-column-column-usage.html
	TablePgColumnColumnUsage = "column_column_usage"
	// TablePgColumnDomainUsage identifies all columns (of a table or a view) that make use of some domain defined in the current database and owned by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-column-domain-usage.html
	TablePgColumnDomainUsage = "column_domain_usage"
	// TablePgColumnOptions contains all the options defined for foreign table columns in the current database.
	// https://www.postgresql.org/docs/13/infoschema-column-options.html
	TablePgColumnOptions = "column_options"
	// TablePgColumnPrivileges identifies all privileges granted on columns to a currently enabled role or by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-column-privileges.html
	TablePgColumnPrivileges = "column_privileges"
	// TablePgColumnUdtUsage identifies all columns that use data types owned by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-column-udt-usage.html
	TablePgColumnUdtUsage = "column_udt_usage"
	// TablePgColumns https://www.postgresql.org/docs/13/infoschema-columns.html
	// https://www.postgresql.org/docs/13/infoschema-columns.html
	TablePgColumns = "columns"
	// TablePgConstraintColumnUsage  identifies all columns in the current database that are used by some constraint.
	// https://www.postgresql.org/docs/13/infoschema-constraint-column-usage.html
	TablePgConstraintColumnUsage = "constraint_column_usage"
	// TablePgConstraintTableUsage identifies all tables in the current database that are used by some constraint and are owned by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-constraint-table-usage.html
	TablePgConstraintTableUsage = "constraint_table_usage"
	// TablePgDataTypePrivileges identifies all data type descriptors that the current user has access to, by way of being the owner of the described object or having some privilege for it.
	// https://www.postgresql.org/docs/13/infoschema-data-type-privileges.html
	TablePgDataTypePrivileges = "data_type_privileges"
	// TablePgDomainConstraints contains all constraints belonging to domains defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-domain-constraints.html
	TablePgDomainConstraints = "domain_constraints"
	// TablePgDomainUdtUsage identifies all domains that are based on data types owned by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-domain-udt-usage.html
	TablePgDomainUdtUsage = "domain_udt_usage"
	// TablePgDomains contains all domains defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-domains.html
	TablePgDomains = "domains"
	// TablePgElementTypes contains the data type descriptors of the elements of arrays.
	// https://www.postgresql.org/docs/13/infoschema-element-types.html
	TablePgElementTypes = "element_types"
	// TablePgEnabledRoles identifies the currently “enabled roles”.
	// https://www.postgresql.org/docs/13/infoschema-enabled-roles.html
	TablePgEnabledRoles = "enabled_roles"
	// TablePgForeignDataWrapperOptions contains all the options defined for foreign-data wrappers in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-data-wrapper-options.html
	TablePgForeignDataWrapperOptions = "foreign_data_wrapper_options"
	// TablePgForeignDataWrappers contains all foreign-data wrappers defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-data-wrappers.html
	TablePgForeignDataWrappers = "foreign_data_wrappers"
	// TablePgForeignServerOptions contains all the options defined for foreign servers in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-server-options.html
	TablePgForeignServerOptions = "foreign_server_options"
	// TablePgForeignServers contains all foreign servers defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-servers.html
	TablePgForeignServers = "foreign_servers"
	// TablePgForeignTableOptions contains all the options defined for foreign tables in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-table-options.html
	TablePgForeignTableOptions = "foreign_table_options"
	// TablePgForeignTales contains all foreign tables defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-foreign-tables.html
	TablePgForeignTales = "foreign_tables"
	// TablePgKeyColumnUsage  identifies all columns in the current database that are restricted by some unique, primary key, or foreign key constraint.
	// https://www.postgresql.org/docs/13/infoschema-key-column-usage.html
	TablePgKeyColumnUsage = "key_column_usage"
	// TablePgParameters  contains information about the parameters (arguments) of all functions in the current database.
	// https://www.postgresql.org/docs/13/infoschema-parameters.html
	TablePgParameters = "parameters"
	// TablePgReferentialConstraints contains all referential (foreign key) constraints in the current database.
	// https://www.postgresql.org/docs/13/infoschema-referential-constraints.html
	TablePgReferentialConstraints = "referential_constraints"
	// TablePgRoleColumnGrants identifies all privileges granted on columns where the grantor or grantee is a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-role-column-grants.html
	TablePgRoleColumnGrants = "role_column_grants"
	// TablePgRoleRoutineGrants identifies all privileges granted on functions where the grantor or grantee is a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-role-routine-grants.html
	TablePgRoleRoutineGrants = "role_routine_grants"
	// TablePgRoleTableGrants identifies all privileges granted on tables or views where the grantor or grantee is a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-role-table-grants.html
	TablePgRoleTableGrants = "role_table_grants"
	// TablePgRoleUdtGrants is intended to identify USAGE privileges granted on user-defined types where the grantor or grantee is a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-role-udt-grants.html
	TablePgRoleUdtGrants = "role_udt_grants"
	// TablePgRoleUsageGrants identifies USAGE privileges granted on various kinds of objects where the grantor or grantee is a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-role-usage-grants.html
	TablePgRoleUsageGrants = "role_usage_grants"
	// TablePgRoutinePrivileges identifies all privileges granted on functions to a currently enabled role or by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-routine-privileges.html
	TablePgRoutinePrivileges = "routine_privileges"
	// TablePgRoutines contains all functions and procedures in the current database.
	// https://www.postgresql.org/docs/13/infoschema-routines.html
	TablePgRoutines = "routines"
	// TablePgSchemata contains all schemas in the current database that the current user has access to (by way of being the owner or having some privilege).
	// https://www.postgresql.org/docs/13/infoschema-schemata.html
	TablePgSchemata = "schemata"
	// TablePgSequences contains all sequences defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-sequences.html
	TablePgSequences = "sequences"
	// TablePgSQLFeatures contains information about which formal features defined in the SQL standard are supported by PostgreSQL.
	// https://www.postgresql.org/docs/13/infoschema-sql-features.html
	TablePgSQLFeatures = "sql_features"
	// TablePgSQLImplementationInfo contains information about various aspects that are left implementation-defined by the SQL standard.
	// https://www.postgresql.org/docs/13/infoschema-sql-implementation-info.html
	TablePgSQLImplementationInfo = "sql_implementation_info"
	// TablePgSQLParts contains information about which of the several parts of the SQL standard are supported by PostgreSQL.
	// https://www.postgresql.org/docs/13/infoschema-sql-parts.html
	TablePgSQLParts = "sql_parts"
	// TablePgSQLSizing contains information about various size limits and maximum values in PostgreSQL.
	// https://www.postgresql.org/docs/13/infoschema-sql-sizing.html
	TablePgSQLSizing = "sql_sizing"
	// TablePgTableConstraints contains all constraints belonging to tables that the current user owns or has some privilege other than SELECT on.
	// https://www.postgresql.org/docs/13/infoschema-table-constraints.html
	TablePgTableConstraints = "table_constraints"
	// TablePgTablePrivileges identifies all privileges granted on tables or views to a currently enabled role or by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-table-privileges.html
	TablePgTablePrivileges = "table_privileges"
	// TablePgTables contains all tables and views defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-tables.html
	TablePgTables = "TABLES"
	// TablePgTransforms contains information about the transforms defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-transforms.html
	TablePgTransforms = "transforms"
	// TablePgTriggeredUpdateColumns For triggers in the current database that specify a column list (like UPDATE OF column1, column2), the view TableTriggeredUpdateColumns identifies these columns.
	// https://www.postgresql.org/docs/13/infoschema-triggered-update-columns.html
	TablePgTriggeredUpdateColumns = "triggered_update_columns"
	// TablePgTriggers contains all triggers defined in the current database on tables and views that the current user owns or has some privilege other than SELECT on.
	// https://www.postgresql.org/docs/13/infoschema-triggers.html
	TablePgTriggers = "triggers"
	// TablePgUdtPrivileges identifies USAGE privileges granted on user-defined types to a currently enabled role or by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-udt-privileges.html
	TablePgUdtPrivileges = "udt_privileges"
	// TablePgUsagePrivileges  identifies USAGE privileges granted on various kinds of objects to a currently enabled role or by a currently enabled role.
	// https://www.postgresql.org/docs/13/infoschema-usage-privileges.html
	TablePgUsagePrivileges = "usage_privileges"
	// TablePgUserDefinedTypes currently contains all composite types defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-user-defined-types.html
	TablePgUserDefinedTypes = "user_defined_types"
	// TablePgUserMappingOptions contains all the options defined for user mappings in the current database.
	// https://www.postgresql.org/docs/13/infoschema-user-mapping-options.html
	TablePgUserMappingOptions = "user_mapping_options"
	// TablePgUserMappings contains all user mappings defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-user-mappings.html
	TablePgUserMappings = "user_mapping"
	// TablePgViewColumnUsage  identifies all columns that are used in the query expression of a view (the SELECT statement that defines the view).
	// https://www.postgresql.org/docs/13/infoschema-view-column-usage.html
	TablePgViewColumnUsage = "view_column_usage"
	// TablePgViewRoutineUsage  identifies all routines (functions and procedures) that are used in the query expression of a view (the SELECT statement that defines the view).
	// https://www.postgresql.org/docs/13/infoschema-view-routine-usage.html
	TablePgViewRoutineUsage = "view_routine_usage"
	// TablePgViewTableUsage identifies all tables that are used in the query expression of a view
	// https://www.postgresql.org/docs/13/infoschema-view-table-usage.html
	TablePgViewTableUsage = "view_table_usage"
	// TablePgViews contains all views defined in the current database.
	// https://www.postgresql.org/docs/13/infoschema-views.html
	TablePgViews = "views"
)

var tableIDMap = map[string]int64{
	TableSchemata:                             autoid.InformationSchemaDBID + 1,
	TableTables:                               autoid.InformationSchemaDBID + 2,
	TableColumns:                              autoid.InformationSchemaDBID + 3,
	tableColumnStatistics:                     autoid.InformationSchemaDBID + 4,
	TableStatistics:                           autoid.InformationSchemaDBID + 5,
	TableCharacterSets:                        autoid.InformationSchemaDBID + 6,
	TableCollations:                           autoid.InformationSchemaDBID + 7,
	tableFiles:                                autoid.InformationSchemaDBID + 8,
	CatalogVal:                                autoid.InformationSchemaDBID + 9,
	TableProfiling:                            autoid.InformationSchemaDBID + 10,
	TablePartitions:                           autoid.InformationSchemaDBID + 11,
	TableKeyColumn:                            autoid.InformationSchemaDBID + 12,
	tableReferConst:                           autoid.InformationSchemaDBID + 13,
	TableSessionVar:                           autoid.InformationSchemaDBID + 14,
	tablePlugins:                              autoid.InformationSchemaDBID + 15,
	TableConstraints:                          autoid.InformationSchemaDBID + 16,
	tableTriggers:                             autoid.InformationSchemaDBID + 17,
	TableUserPrivileges:                       autoid.InformationSchemaDBID + 18,
	tableSchemaPrivileges:                     autoid.InformationSchemaDBID + 19,
	tableTablePrivileges:                      autoid.InformationSchemaDBID + 20,
	tableColumnPrivileges:                     autoid.InformationSchemaDBID + 21,
	TableEngines:                              autoid.InformationSchemaDBID + 22,
	TableViews:                                autoid.InformationSchemaDBID + 23,
	tableRoutines:                             autoid.InformationSchemaDBID + 24,
	tableParameters:                           autoid.InformationSchemaDBID + 25,
	tableEvents:                               autoid.InformationSchemaDBID + 26,
	tableGlobalStatus:                         autoid.InformationSchemaDBID + 27,
	tableGlobalVariables:                      autoid.InformationSchemaDBID + 28,
	tableSessionStatus:                        autoid.InformationSchemaDBID + 29,
	tableOptimizerTrace:                       autoid.InformationSchemaDBID + 30,
	tableTableSpaces:                          autoid.InformationSchemaDBID + 31,
	TableCollationCharacterSetApplicability:   autoid.InformationSchemaDBID + 32,
	TableProcesslist:                          autoid.InformationSchemaDBID + 33,
	TableTiDBIndexes:                          autoid.InformationSchemaDBID + 34,
	TableSlowQuery:                            autoid.InformationSchemaDBID + 35,
	TableTiDBHotRegions:                       autoid.InformationSchemaDBID + 36,
	tableTiKVStoreStatus:                      autoid.InformationSchemaDBID + 37,
	TableAnalyzeStatus:                        autoid.InformationSchemaDBID + 38,
	tableTiKVRegionStatus:                     autoid.InformationSchemaDBID + 39,
	TableTiKVRegionPeers:                      autoid.InformationSchemaDBID + 40,
	TableTiDBServersInfo:                      autoid.InformationSchemaDBID + 41,
	TableClusterInfo:                          autoid.InformationSchemaDBID + 42,
	TableClusterConfig:                        autoid.InformationSchemaDBID + 43,
	TableClusterLoad:                          autoid.InformationSchemaDBID + 44,
	TableTiFlashReplica:                       autoid.InformationSchemaDBID + 45,
	ClusterTableSlowLog:                       autoid.InformationSchemaDBID + 46,
	ClusterTableProcesslist:                   autoid.InformationSchemaDBID + 47,
	TableClusterLog:                           autoid.InformationSchemaDBID + 48,
	TableClusterHardware:                      autoid.InformationSchemaDBID + 49,
	TableClusterSystemInfo:                    autoid.InformationSchemaDBID + 50,
	TableInspectionResult:                     autoid.InformationSchemaDBID + 51,
	TableMetricSummary:                        autoid.InformationSchemaDBID + 52,
	TableMetricSummaryByLabel:                 autoid.InformationSchemaDBID + 53,
	TableMetricTables:                         autoid.InformationSchemaDBID + 54,
	TableInspectionSummary:                    autoid.InformationSchemaDBID + 55,
	TableInspectionRules:                      autoid.InformationSchemaDBID + 56,
	TableDDLJobs:                              autoid.InformationSchemaDBID + 57,
	TableSequences:                            autoid.InformationSchemaDBID + 58,
	TableStatementsSummary:                    autoid.InformationSchemaDBID + 59,
	TableStatementsSummaryHistory:             autoid.InformationSchemaDBID + 60,
	ClusterTableStatementsSummary:             autoid.InformationSchemaDBID + 61,
	ClusterTableStatementsSummaryHistory:      autoid.InformationSchemaDBID + 62,
	TableStorageStats:                         autoid.InformationSchemaDBID + 63,
	TableTiFlashTables:                        autoid.InformationSchemaDBID + 64,
	TableTiFlashSegments:                      autoid.InformationSchemaDBID + 65,
	TablePgInformationsSchemaCatalogName:      autoid.InformationSchemaDBID + 66,
	TablePgAdministrableRoleAuthorizations:    autoid.InformationSchemaDBID + 67,
	TablePgApplicableRole:                     autoid.InformationSchemaDBID + 68,
	TablePgAttributes:                         autoid.InformationSchemaDBID + 69,
	TablePgCharacterSets:                      autoid.InformationSchemaDBID + 70,
	TablePgCheckConstraintRoutineUsage:        autoid.InformationSchemaDBID + 71,
	TablePgCheckConstraints:                   autoid.InformationSchemaDBID + 72,
	TablePgCollationCharacterSetApplicability: autoid.InformationSchemaDBID + 73,
	TablePgCollations:                         autoid.InformationSchemaDBID + 74,
	TablePgColumnColumnUsage:                  autoid.InformationSchemaDBID + 75,
	TablePgColumnDomainUsage:                  autoid.InformationSchemaDBID + 76,
	TablePgColumnOptions:                      autoid.InformationSchemaDBID + 77,
	TablePgColumnPrivileges:                   autoid.InformationSchemaDBID + 78,
	TablePgColumnUdtUsage:                     autoid.InformationSchemaDBID + 79,
	TablePgColumns:                            autoid.InformationSchemaDBID + 80,
	TablePgConstraintColumnUsage:              autoid.InformationSchemaDBID + 81,
	TablePgConstraintTableUsage:               autoid.InformationSchemaDBID + 82,
	TablePgDataTypePrivileges:                 autoid.InformationSchemaDBID + 83,
	TablePgDomainConstraints:                  autoid.InformationSchemaDBID + 84,
	TablePgDomainUdtUsage:                     autoid.InformationSchemaDBID + 85,
	TablePgDomains:                            autoid.InformationSchemaDBID + 86,
	TablePgElementTypes:                       autoid.InformationSchemaDBID + 87,
	TablePgEnabledRoles:                       autoid.InformationSchemaDBID + 88,
	TablePgForeignDataWrapperOptions:          autoid.InformationSchemaDBID + 89,
	TablePgForeignDataWrappers:                autoid.InformationSchemaDBID + 90,
	TablePgForeignServerOptions:               autoid.InformationSchemaDBID + 91,
	TablePgForeignServers:                     autoid.InformationSchemaDBID + 92,
	TablePgForeignTableOptions:                autoid.InformationSchemaDBID + 93,
	TablePgForeignTales:                       autoid.InformationSchemaDBID + 94,
	TablePgKeyColumnUsage:                     autoid.InformationSchemaDBID + 95,
	TablePgParameters:                         autoid.InformationSchemaDBID + 96,
	TablePgReferentialConstraints:             autoid.InformationSchemaDBID + 97,
	TablePgRoleColumnGrants:                   autoid.InformationSchemaDBID + 98,
	TablePgRoleRoutineGrants:                  autoid.InformationSchemaDBID + 99,
	TablePgRoleTableGrants:                    autoid.InformationSchemaDBID + 100,
	TablePgRoleUdtGrants:                      autoid.InformationSchemaDBID + 101,
	TablePgRoleUsageGrants:                    autoid.InformationSchemaDBID + 102,
	TablePgRoutinePrivileges:                  autoid.InformationSchemaDBID + 103,
	TablePgRoutines:                           autoid.InformationSchemaDBID + 104,
	TablePgSchemata:                           autoid.InformationSchemaDBID + 105,
	TablePgSequences:                          autoid.InformationSchemaDBID + 106,
	TablePgSQLFeatures:                        autoid.InformationSchemaDBID + 107,
	TablePgSQLImplementationInfo:              autoid.InformationSchemaDBID + 108,
	TablePgSQLParts:                           autoid.InformationSchemaDBID + 109,
	TablePgSQLSizing:                          autoid.InformationSchemaDBID + 110,
	TablePgTableConstraints:                   autoid.InformationSchemaDBID + 111,
	TablePgTablePrivileges:                    autoid.InformationSchemaDBID + 112,
	TablePgTables:                             autoid.InformationSchemaDBID + 113,
	TablePgTransforms:                         autoid.InformationSchemaDBID + 114,
	TablePgTriggeredUpdateColumns:             autoid.InformationSchemaDBID + 115,
	TablePgTriggers:                           autoid.InformationSchemaDBID + 116,
	TablePgUdtPrivileges:                      autoid.InformationSchemaDBID + 117,
	TablePgUsagePrivileges:                    autoid.InformationSchemaDBID + 118,
	TablePgUserDefinedTypes:                   autoid.InformationSchemaDBID + 119,
	TablePgUserMappingOptions:                 autoid.InformationSchemaDBID + 120,
	TablePgUserMappings:                       autoid.InformationSchemaDBID + 121,
	TablePgViewColumnUsage:                    autoid.InformationSchemaDBID + 122,
	TablePgViewRoutineUsage:                   autoid.InformationSchemaDBID + 123,
	TablePgViewTableUsage:                     autoid.InformationSchemaDBID + 124,
	TablePgViews:                              autoid.InformationSchemaDBID + 125,
}

type columnInfo struct {
	name    string
	tp      byte
	size    int
	decimal int
	flag    uint
	deflt   interface{}
	comment string
}

func buildColumnInfo(col columnInfo) *model.ColumnInfo {
	mCharset := charset.CharsetBin
	mCollation := charset.CharsetBin
	if col.tp == mysql.TypeVarchar || col.tp == mysql.TypeBlob || col.tp == mysql.TypeLongBlob {
		mCharset = charset.CharsetUTF8MB4
		mCollation = charset.CollationUTF8MB4
	}
	fieldType := types.FieldType{
		Charset: mCharset,
		Collate: mCollation,
		Tp:      col.tp,
		Flen:    col.size,
		Decimal: col.decimal,
		Flag:    col.flag,
	}
	return &model.ColumnInfo{
		Name:         model.NewCIStr(col.name),
		FieldType:    fieldType,
		State:        model.StatePublic,
		DefaultValue: col.deflt,
		Comment:      col.comment,
	}
}

func buildTableMeta(tableName string, cs []columnInfo) *model.TableInfo {
	cols := make([]*model.ColumnInfo, 0, len(cs))
	for _, c := range cs {
		cols = append(cols, buildColumnInfo(c))
	}
	for i, col := range cols {
		col.Offset = i
	}
	return &model.TableInfo{
		Name:    model.NewCIStr(tableName),
		Columns: cols,
		State:   model.StatePublic,
		Charset: mysql.DefaultCharset,
		Collate: mysql.DefaultCollationName,
	}
}

var schemataCols = []columnInfo{
	{name: "CATALOG_NAME", tp: mysql.TypeVarchar, size: 512},
	{name: "SCHEMA_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "DEFAULT_CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "DEFAULT_COLLATION_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "SQL_PATH", tp: mysql.TypeVarchar, size: 512},
}

var tablesCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "ENGINE", tp: mysql.TypeVarchar, size: 64},
	{name: "VERSION", tp: mysql.TypeLonglong, size: 21},
	{name: "ROW_FORMAT", tp: mysql.TypeVarchar, size: 10},
	{name: "TABLE_ROWS", tp: mysql.TypeLonglong, size: 21},
	{name: "AVG_ROW_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "MAX_DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_FREE", tp: mysql.TypeLonglong, size: 21},
	{name: "AUTO_INCREMENT", tp: mysql.TypeLonglong, size: 21},
	{name: "CREATE_TIME", tp: mysql.TypeDatetime, size: 19},
	{name: "UPDATE_TIME", tp: mysql.TypeDatetime, size: 19},
	{name: "CHECK_TIME", tp: mysql.TypeDatetime, size: 19},
	{name: "TABLE_COLLATION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag, deflt: "utf8_bin"},
	{name: "CHECKSUM", tp: mysql.TypeLonglong, size: 21},
	{name: "CREATE_OPTIONS", tp: mysql.TypeVarchar, size: 255},
	{name: "TABLE_COMMENT", tp: mysql.TypeVarchar, size: 2048},
	{name: "TIDB_TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "TIDB_ROW_ID_SHARDING_INFO", tp: mysql.TypeVarchar, size: 255},
}

// See: http://dev.mysql.com/doc/refman/5.7/en/columns-table.html
var columnsCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "ORDINAL_POSITION", tp: mysql.TypeLonglong, size: 64},
	{name: "COLUMN_DEFAULT", tp: mysql.TypeBlob, size: 196606},
	{name: "IS_NULLABLE", tp: mysql.TypeVarchar, size: 3},
	{name: "DATA_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "CHARACTER_MAXIMUM_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "CHARACTER_OCTET_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "NUMERIC_PRECISION", tp: mysql.TypeLonglong, size: 21},
	{name: "NUMERIC_SCALE", tp: mysql.TypeLonglong, size: 21},
	{name: "DATETIME_PRECISION", tp: mysql.TypeLonglong, size: 21},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "COLLATION_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "COLUMN_TYPE", tp: mysql.TypeBlob, size: 196606},
	{name: "COLUMN_KEY", tp: mysql.TypeVarchar, size: 3},
	{name: "EXTRA", tp: mysql.TypeVarchar, size: 30},
	{name: "PRIVILEGES", tp: mysql.TypeVarchar, size: 80},
	{name: "COLUMN_COMMENT", tp: mysql.TypeVarchar, size: 1024},
	{name: "GENERATION_EXPRESSION", tp: mysql.TypeBlob, size: 589779, flag: mysql.NotNullFlag},
}

var columnStatisticsCols = []columnInfo{
	{name: "SCHEMA_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "HISTOGRAM", tp: mysql.TypeJSON, size: 51},
}

var statisticsCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "NON_UNIQUE", tp: mysql.TypeVarchar, size: 1},
	{name: "INDEX_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "INDEX_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "SEQ_IN_INDEX", tp: mysql.TypeLonglong, size: 2},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 21},
	{name: "COLLATION", tp: mysql.TypeVarchar, size: 1},
	{name: "CARDINALITY", tp: mysql.TypeLonglong, size: 21},
	{name: "SUB_PART", tp: mysql.TypeLonglong, size: 3},
	{name: "PACKED", tp: mysql.TypeVarchar, size: 10},
	{name: "NULLABLE", tp: mysql.TypeVarchar, size: 3},
	{name: "INDEX_TYPE", tp: mysql.TypeVarchar, size: 16},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 16},
	{name: "INDEX_COMMENT", tp: mysql.TypeVarchar, size: 1024},
	{name: "IS_VISIBLE", tp: mysql.TypeVarchar, size: 3},
	{name: "Expression", tp: mysql.TypeVarchar, size: 64},
}

var profilingCols = []columnInfo{
	{name: "QUERY_ID", tp: mysql.TypeLong, size: 20},
	{name: "SEQ", tp: mysql.TypeLong, size: 20},
	{name: "STATE", tp: mysql.TypeVarchar, size: 30},
	{name: "DURATION", tp: mysql.TypeNewDecimal, size: 9},
	{name: "CPU_USER", tp: mysql.TypeNewDecimal, size: 9},
	{name: "CPU_SYSTEM", tp: mysql.TypeNewDecimal, size: 9},
	{name: "CONTEXT_VOLUNTARY", tp: mysql.TypeLong, size: 20},
	{name: "CONTEXT_INVOLUNTARY", tp: mysql.TypeLong, size: 20},
	{name: "BLOCK_OPS_IN", tp: mysql.TypeLong, size: 20},
	{name: "BLOCK_OPS_OUT", tp: mysql.TypeLong, size: 20},
	{name: "MESSAGES_SENT", tp: mysql.TypeLong, size: 20},
	{name: "MESSAGES_RECEIVED", tp: mysql.TypeLong, size: 20},
	{name: "PAGE_FAULTS_MAJOR", tp: mysql.TypeLong, size: 20},
	{name: "PAGE_FAULTS_MINOR", tp: mysql.TypeLong, size: 20},
	{name: "SWAPS", tp: mysql.TypeLong, size: 20},
	{name: "SOURCE_FUNCTION", tp: mysql.TypeVarchar, size: 30},
	{name: "SOURCE_FILE", tp: mysql.TypeVarchar, size: 20},
	{name: "SOURCE_LINE", tp: mysql.TypeLong, size: 20},
}

var charsetCols = []columnInfo{
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "DEFAULT_COLLATE_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "DESCRIPTION", tp: mysql.TypeVarchar, size: 60},
	{name: "MAXLEN", tp: mysql.TypeLonglong, size: 3},
}

var collationsCols = []columnInfo{
	{name: "COLLATION_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 32},
	{name: "ID", tp: mysql.TypeLonglong, size: 11},
	{name: "IS_DEFAULT", tp: mysql.TypeVarchar, size: 3},
	{name: "IS_COMPILED", tp: mysql.TypeVarchar, size: 3},
	{name: "SORTLEN", tp: mysql.TypeLonglong, size: 3},
}

var keyColumnUsageCols = []columnInfo{
	{name: "CONSTRAINT_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "CONSTRAINT_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "CONSTRAINT_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ORDINAL_POSITION", tp: mysql.TypeLonglong, size: 10, flag: mysql.NotNullFlag},
	{name: "POSITION_IN_UNIQUE_CONSTRAINT", tp: mysql.TypeLonglong, size: 10},
	{name: "REFERENCED_TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "REFERENCED_TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "REFERENCED_COLUMN_NAME", tp: mysql.TypeVarchar, size: 64},
}

// See http://dev.mysql.com/doc/refman/5.7/en/referential-constraints-table.html
var referConstCols = []columnInfo{
	{name: "CONSTRAINT_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "CONSTRAINT_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "CONSTRAINT_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "UNIQUE_CONSTRAINT_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "UNIQUE_CONSTRAINT_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "UNIQUE_CONSTRAINT_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "MATCH_OPTION", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "UPDATE_RULE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "DELETE_RULE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "REFERENCED_TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
}

// See http://dev.mysql.com/doc/refman/5.7/en/variables-table.html
var sessionVarCols = []columnInfo{
	{name: "VARIABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "VARIABLE_VALUE", tp: mysql.TypeVarchar, size: 1024},
}

// See https://dev.mysql.com/doc/refman/5.7/en/plugins-table.html
var pluginsCols = []columnInfo{
	{name: "PLUGIN_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "PLUGIN_VERSION", tp: mysql.TypeVarchar, size: 20},
	{name: "PLUGIN_STATUS", tp: mysql.TypeVarchar, size: 10},
	{name: "PLUGIN_TYPE", tp: mysql.TypeVarchar, size: 80},
	{name: "PLUGIN_TYPE_VERSION", tp: mysql.TypeVarchar, size: 20},
	{name: "PLUGIN_LIBRARY", tp: mysql.TypeVarchar, size: 64},
	{name: "PLUGIN_LIBRARY_VERSION", tp: mysql.TypeVarchar, size: 20},
	{name: "PLUGIN_AUTHOR", tp: mysql.TypeVarchar, size: 64},
	{name: "PLUGIN_DESCRIPTION", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: "PLUGIN_LICENSE", tp: mysql.TypeVarchar, size: 80},
	{name: "LOAD_OPTION", tp: mysql.TypeVarchar, size: 64},
}

// See https://dev.mysql.com/doc/refman/5.7/en/partitions-table.html
var partitionsCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "PARTITION_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "SUBPARTITION_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "PARTITION_ORDINAL_POSITION", tp: mysql.TypeLonglong, size: 21},
	{name: "SUBPARTITION_ORDINAL_POSITION", tp: mysql.TypeLonglong, size: 21},
	{name: "PARTITION_METHOD", tp: mysql.TypeVarchar, size: 18},
	{name: "SUBPARTITION_METHOD", tp: mysql.TypeVarchar, size: 12},
	{name: "PARTITION_EXPRESSION", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: "SUBPARTITION_EXPRESSION", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: "PARTITION_DESCRIPTION", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: "TABLE_ROWS", tp: mysql.TypeLonglong, size: 21},
	{name: "AVG_ROW_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "MAX_DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_FREE", tp: mysql.TypeLonglong, size: 21},
	{name: "CREATE_TIME", tp: mysql.TypeDatetime},
	{name: "UPDATE_TIME", tp: mysql.TypeDatetime},
	{name: "CHECK_TIME", tp: mysql.TypeDatetime},
	{name: "CHECKSUM", tp: mysql.TypeLonglong, size: 21},
	{name: "PARTITION_COMMENT", tp: mysql.TypeVarchar, size: 80},
	{name: "NODEGROUP", tp: mysql.TypeVarchar, size: 12},
	{name: "TABLESPACE_NAME", tp: mysql.TypeVarchar, size: 64},
}

var tableConstraintsCols = []columnInfo{
	{name: "CONSTRAINT_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "CONSTRAINT_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "CONSTRAINT_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "CONSTRAINT_TYPE", tp: mysql.TypeVarchar, size: 64},
}

var tableTriggersCols = []columnInfo{
	{name: "TRIGGER_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "TRIGGER_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TRIGGER_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "EVENT_MANIPULATION", tp: mysql.TypeVarchar, size: 6},
	{name: "EVENT_OBJECT_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "EVENT_OBJECT_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "EVENT_OBJECT_TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "ACTION_ORDER", tp: mysql.TypeLonglong, size: 4},
	{name: "ACTION_CONDITION", tp: mysql.TypeBlob, size: -1},
	{name: "ACTION_STATEMENT", tp: mysql.TypeBlob, size: -1},
	{name: "ACTION_ORIENTATION", tp: mysql.TypeVarchar, size: 9},
	{name: "ACTION_TIMING", tp: mysql.TypeVarchar, size: 6},
	{name: "ACTION_REFERENCE_OLD_TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "ACTION_REFERENCE_NEW_TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "ACTION_REFERENCE_OLD_ROW", tp: mysql.TypeVarchar, size: 3},
	{name: "ACTION_REFERENCE_NEW_ROW", tp: mysql.TypeVarchar, size: 3},
	{name: "CREATED", tp: mysql.TypeDatetime, size: 2},
	{name: "SQL_MODE", tp: mysql.TypeVarchar, size: 8192},
	{name: "DEFINER", tp: mysql.TypeVarchar, size: 77},
	{name: "CHARACTER_SET_CLIENT", tp: mysql.TypeVarchar, size: 32},
	{name: "COLLATION_CONNECTION", tp: mysql.TypeVarchar, size: 32},
	{name: "DATABASE_COLLATION", tp: mysql.TypeVarchar, size: 32},
}

var tableUserPrivilegesCols = []columnInfo{
	{name: "GRANTEE", tp: mysql.TypeVarchar, size: 81},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512},
	{name: "PRIVILEGE_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "IS_GRANTABLE", tp: mysql.TypeVarchar, size: 3},
}

var tableSchemaPrivilegesCols = []columnInfo{
	{name: "GRANTEE", tp: mysql.TypeVarchar, size: 81, flag: mysql.NotNullFlag},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "PRIVILEGE_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "IS_GRANTABLE", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
}

var tableTablePrivilegesCols = []columnInfo{
	{name: "GRANTEE", tp: mysql.TypeVarchar, size: 81, flag: mysql.NotNullFlag},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "PRIVILEGE_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "IS_GRANTABLE", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
}

var tableColumnPrivilegesCols = []columnInfo{
	{name: "GRANTEE", tp: mysql.TypeVarchar, size: 81, flag: mysql.NotNullFlag},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "PRIVILEGE_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "IS_GRANTABLE", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
}

var tableEnginesCols = []columnInfo{
	{name: "ENGINE", tp: mysql.TypeVarchar, size: 64},
	{name: "SUPPORT", tp: mysql.TypeVarchar, size: 8},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 80},
	{name: "TRANSACTIONS", tp: mysql.TypeVarchar, size: 3},
	{name: "XA", tp: mysql.TypeVarchar, size: 3},
	{name: "SAVEPOINTS", tp: mysql.TypeVarchar, size: 3},
}

var tableViewsCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "VIEW_DEFINITION", tp: mysql.TypeLongBlob, flag: mysql.NotNullFlag},
	{name: "CHECK_OPTION", tp: mysql.TypeVarchar, size: 8, flag: mysql.NotNullFlag},
	{name: "IS_UPDATABLE", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
	{name: "DEFINER", tp: mysql.TypeVarchar, size: 77, flag: mysql.NotNullFlag},
	{name: "SECURITY_TYPE", tp: mysql.TypeVarchar, size: 7, flag: mysql.NotNullFlag},
	{name: "CHARACTER_SET_CLIENT", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "COLLATION_CONNECTION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
}

var tableRoutinesCols = []columnInfo{
	{name: "SPECIFIC_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ROUTINE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "ROUTINE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ROUTINE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ROUTINE_TYPE", tp: mysql.TypeVarchar, size: 9, flag: mysql.NotNullFlag},
	{name: "DATA_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "CHARACTER_MAXIMUM_LENGTH", tp: mysql.TypeLong, size: 21},
	{name: "CHARACTER_OCTET_LENGTH", tp: mysql.TypeLong, size: 21},
	{name: "NUMERIC_PRECISION", tp: mysql.TypeLonglong, size: 21},
	{name: "NUMERIC_SCALE", tp: mysql.TypeLong, size: 21},
	{name: "DATETIME_PRECISION", tp: mysql.TypeLonglong, size: 21},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "COLLATION_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "DTD_IDENTIFIER", tp: mysql.TypeLongBlob},
	{name: "ROUTINE_BODY", tp: mysql.TypeVarchar, size: 8, flag: mysql.NotNullFlag},
	{name: "ROUTINE_DEFINITION", tp: mysql.TypeLongBlob},
	{name: "EXTERNAL_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "EXTERNAL_LANGUAGE", tp: mysql.TypeVarchar, size: 64},
	{name: "PARAMETER_STYLE", tp: mysql.TypeVarchar, size: 8, flag: mysql.NotNullFlag},
	{name: "IS_DETERMINISTIC", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
	{name: "SQL_DATA_ACCESS", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "SQL_PATH", tp: mysql.TypeVarchar, size: 64},
	{name: "SECURITY_TYPE", tp: mysql.TypeVarchar, size: 7, flag: mysql.NotNullFlag},
	{name: "CREATED", tp: mysql.TypeDatetime, flag: mysql.NotNullFlag, deflt: "0000-00-00 00:00:00"},
	{name: "LAST_ALTERED", tp: mysql.TypeDatetime, flag: mysql.NotNullFlag, deflt: "0000-00-00 00:00:00"},
	{name: "SQL_MODE", tp: mysql.TypeVarchar, size: 8192, flag: mysql.NotNullFlag},
	{name: "ROUTINE_COMMENT", tp: mysql.TypeLongBlob},
	{name: "DEFINER", tp: mysql.TypeVarchar, size: 77, flag: mysql.NotNullFlag},
	{name: "CHARACTER_SET_CLIENT", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "COLLATION_CONNECTION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "DATABASE_COLLATION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
}

var tableParametersCols = []columnInfo{
	{name: "SPECIFIC_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "SPECIFIC_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "SPECIFIC_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ORDINAL_POSITION", tp: mysql.TypeVarchar, size: 21, flag: mysql.NotNullFlag},
	{name: "PARAMETER_MODE", tp: mysql.TypeVarchar, size: 5},
	{name: "PARAMETER_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "DATA_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "CHARACTER_MAXIMUM_LENGTH", tp: mysql.TypeVarchar, size: 21},
	{name: "CHARACTER_OCTET_LENGTH", tp: mysql.TypeVarchar, size: 21},
	{name: "NUMERIC_PRECISION", tp: mysql.TypeVarchar, size: 21},
	{name: "NUMERIC_SCALE", tp: mysql.TypeVarchar, size: 21},
	{name: "DATETIME_PRECISION", tp: mysql.TypeVarchar, size: 21},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "COLLATION_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "DTD_IDENTIFIER", tp: mysql.TypeLongBlob, flag: mysql.NotNullFlag},
	{name: "ROUTINE_TYPE", tp: mysql.TypeVarchar, size: 9, flag: mysql.NotNullFlag},
}

var tableEventsCols = []columnInfo{
	{name: "EVENT_CATALOG", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "EVENT_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "EVENT_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "DEFINER", tp: mysql.TypeVarchar, size: 77, flag: mysql.NotNullFlag},
	{name: "TIME_ZONE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "EVENT_BODY", tp: mysql.TypeVarchar, size: 8, flag: mysql.NotNullFlag},
	{name: "EVENT_DEFINITION", tp: mysql.TypeLongBlob},
	{name: "EVENT_TYPE", tp: mysql.TypeVarchar, size: 9, flag: mysql.NotNullFlag},
	{name: "EXECUTE_AT", tp: mysql.TypeDatetime},
	{name: "INTERVAL_VALUE", tp: mysql.TypeVarchar, size: 256},
	{name: "INTERVAL_FIELD", tp: mysql.TypeVarchar, size: 18},
	{name: "SQL_MODE", tp: mysql.TypeVarchar, size: 8192, flag: mysql.NotNullFlag},
	{name: "STARTS", tp: mysql.TypeDatetime},
	{name: "ENDS", tp: mysql.TypeDatetime},
	{name: "STATUS", tp: mysql.TypeVarchar, size: 18, flag: mysql.NotNullFlag},
	{name: "ON_COMPLETION", tp: mysql.TypeVarchar, size: 12, flag: mysql.NotNullFlag},
	{name: "CREATED", tp: mysql.TypeDatetime, flag: mysql.NotNullFlag, deflt: "0000-00-00 00:00:00"},
	{name: "LAST_ALTERED", tp: mysql.TypeDatetime, flag: mysql.NotNullFlag, deflt: "0000-00-00 00:00:00"},
	{name: "LAST_EXECUTED", tp: mysql.TypeDatetime},
	{name: "EVENT_COMMENT", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ORIGINATOR", tp: mysql.TypeLong, size: 10, flag: mysql.NotNullFlag, deflt: 0},
	{name: "CHARACTER_SET_CLIENT", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "COLLATION_CONNECTION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "DATABASE_COLLATION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
}

var tableGlobalStatusCols = []columnInfo{
	{name: "VARIABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "VARIABLE_VALUE", tp: mysql.TypeVarchar, size: 1024},
}

var tableGlobalVariablesCols = []columnInfo{
	{name: "VARIABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "VARIABLE_VALUE", tp: mysql.TypeVarchar, size: 1024},
}

var tableSessionStatusCols = []columnInfo{
	{name: "VARIABLE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "VARIABLE_VALUE", tp: mysql.TypeVarchar, size: 1024},
}

var tableOptimizerTraceCols = []columnInfo{
	{name: "QUERY", tp: mysql.TypeLongBlob, flag: mysql.NotNullFlag, deflt: ""},
	{name: "TRACE", tp: mysql.TypeLongBlob, flag: mysql.NotNullFlag, deflt: ""},
	{name: "MISSING_BYTES_BEYOND_MAX_MEM_SIZE", tp: mysql.TypeShort, size: 20, flag: mysql.NotNullFlag, deflt: 0},
	{name: "INSUFFICIENT_PRIVILEGES", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, deflt: 0},
}

var tableTableSpacesCols = []columnInfo{
	{name: "TABLESPACE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, deflt: ""},
	{name: "ENGINE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, deflt: ""},
	{name: "TABLESPACE_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "LOGFILE_GROUP_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "EXTENT_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "AUTOEXTEND_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "MAXIMUM_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "NODEGROUP_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "TABLESPACE_COMMENT", tp: mysql.TypeVarchar, size: 2048},
}

var tableCollationCharacterSetApplicabilityCols = []columnInfo{
	{name: "COLLATION_NAME", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
}

var tableProcesslistCols = []columnInfo{
	{name: "ID", tp: mysql.TypeLonglong, size: 21, flag: mysql.NotNullFlag | mysql.UnsignedFlag, deflt: 0},
	{name: "USER", tp: mysql.TypeVarchar, size: 16, flag: mysql.NotNullFlag, deflt: ""},
	{name: "HOST", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, deflt: ""},
	{name: "DB", tp: mysql.TypeVarchar, size: 64},
	{name: "COMMAND", tp: mysql.TypeVarchar, size: 16, flag: mysql.NotNullFlag, deflt: ""},
	{name: "TIME", tp: mysql.TypeLong, size: 7, flag: mysql.NotNullFlag, deflt: 0},
	{name: "STATE", tp: mysql.TypeVarchar, size: 7},
	{name: "INFO", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: "DIGEST", tp: mysql.TypeVarchar, size: 64, deflt: ""},
	{name: "MEM", tp: mysql.TypeLonglong, size: 21, flag: mysql.UnsignedFlag},
	{name: "TxnStart", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, deflt: ""},
}

var tableTiDBIndexesCols = []columnInfo{
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "NON_UNIQUE", tp: mysql.TypeLonglong, size: 21},
	{name: "KEY_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "SEQ_IN_INDEX", tp: mysql.TypeLonglong, size: 21},
	{name: "COLUMN_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "SUB_PART", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_COMMENT", tp: mysql.TypeVarchar, size: 2048},
	{name: "Expression", tp: mysql.TypeVarchar, size: 64},
	{name: "INDEX_ID", tp: mysql.TypeLonglong, size: 21},
}

var slowQueryCols = []columnInfo{
	{name: variable.SlowLogTimeStr, tp: mysql.TypeTimestamp, size: 26, decimal: 6},
	{name: variable.SlowLogTxnStartTSStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: variable.SlowLogUserStr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogHostStr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogConnIDStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: variable.SlowLogExecRetryCount, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: variable.SlowLogExecRetryTime, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogQueryTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogParseTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCompileTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogRewriteTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogPreprocSubQueriesStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: variable.SlowLogPreProcSubQueryTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.PreWriteTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.WaitPrewriteBinlogTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.CommitTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.GetCommitTSTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.CommitBackoffTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.BackoffTypesStr, tp: mysql.TypeVarchar, size: 64},
	{name: execdetails.ResolveLockTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.LocalLatchWaitTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.WriteKeysStr, tp: mysql.TypeLonglong, size: 22},
	{name: execdetails.WriteSizeStr, tp: mysql.TypeLonglong, size: 22},
	{name: execdetails.PrewriteRegionStr, tp: mysql.TypeLonglong, size: 22},
	{name: execdetails.TxnRetryStr, tp: mysql.TypeLonglong, size: 22},
	{name: execdetails.CopTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.ProcessTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.WaitTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.BackoffTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.LockKeysTimeStr, tp: mysql.TypeDouble, size: 22},
	{name: execdetails.RequestCountStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: execdetails.TotalKeysStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: execdetails.ProcessKeysStr, tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: variable.SlowLogDBStr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogIndexNamesStr, tp: mysql.TypeVarchar, size: 100},
	{name: variable.SlowLogIsInternalStr, tp: mysql.TypeTiny, size: 1},
	{name: variable.SlowLogDigestStr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogStatsInfoStr, tp: mysql.TypeVarchar, size: 512},
	{name: variable.SlowLogCopProcAvg, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopProcP90, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopProcMax, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopProcAddr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogCopWaitAvg, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopWaitP90, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopWaitMax, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogCopWaitAddr, tp: mysql.TypeVarchar, size: 64},
	{name: variable.SlowLogMemMax, tp: mysql.TypeLonglong, size: 20},
	{name: variable.SlowLogDiskMax, tp: mysql.TypeLonglong, size: 20},
	{name: variable.SlowLogKVTotal, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogPDTotal, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogBackoffTotal, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogWriteSQLRespTotal, tp: mysql.TypeDouble, size: 22},
	{name: variable.SlowLogBackoffDetail, tp: mysql.TypeVarchar, size: 4096},
	{name: variable.SlowLogPrepared, tp: mysql.TypeTiny, size: 1},
	{name: variable.SlowLogSucc, tp: mysql.TypeTiny, size: 1},
	{name: variable.SlowLogPlanFromCache, tp: mysql.TypeTiny, size: 1},
	{name: variable.SlowLogPlan, tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: variable.SlowLogPlanDigest, tp: mysql.TypeVarchar, size: 128},
	{name: variable.SlowLogPrevStmt, tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
	{name: variable.SlowLogQuerySQLStr, tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
}

// TableTiDBHotRegionsCols is TiDB hot region mem table columns.
var TableTiDBHotRegionsCols = []columnInfo{
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "DB_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "INDEX_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "REGION_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "MAX_HOT_DEGREE", tp: mysql.TypeLonglong, size: 21},
	{name: "REGION_COUNT", tp: mysql.TypeLonglong, size: 21},
	{name: "FLOW_BYTES", tp: mysql.TypeLonglong, size: 21},
}

var tableTiKVStoreStatusCols = []columnInfo{
	{name: "STORE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "ADDRESS", tp: mysql.TypeVarchar, size: 64},
	{name: "STORE_STATE", tp: mysql.TypeLonglong, size: 21},
	{name: "STORE_STATE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "LABEL", tp: mysql.TypeJSON, size: 51},
	{name: "VERSION", tp: mysql.TypeVarchar, size: 64},
	{name: "CAPACITY", tp: mysql.TypeVarchar, size: 64},
	{name: "AVAILABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "LEADER_COUNT", tp: mysql.TypeLonglong, size: 21},
	{name: "LEADER_WEIGHT", tp: mysql.TypeDouble, size: 22},
	{name: "LEADER_SCORE", tp: mysql.TypeDouble, size: 22},
	{name: "LEADER_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "REGION_COUNT", tp: mysql.TypeLonglong, size: 21},
	{name: "REGION_WEIGHT", tp: mysql.TypeDouble, size: 22},
	{name: "REGION_SCORE", tp: mysql.TypeDouble, size: 22},
	{name: "REGION_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "START_TS", tp: mysql.TypeDatetime},
	{name: "LAST_HEARTBEAT_TS", tp: mysql.TypeDatetime},
	{name: "UPTIME", tp: mysql.TypeVarchar, size: 64},
}

var tableAnalyzeStatusCols = []columnInfo{
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "PARTITION_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "JOB_INFO", tp: mysql.TypeVarchar, size: 64},
	{name: "PROCESSED_ROWS", tp: mysql.TypeLonglong, size: 20, flag: mysql.UnsignedFlag},
	{name: "START_TIME", tp: mysql.TypeDatetime},
	{name: "STATE", tp: mysql.TypeVarchar, size: 64},
}

var tableTiKVRegionStatusCols = []columnInfo{
	{name: "REGION_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "START_KEY", tp: mysql.TypeBlob, size: types.UnspecifiedLength},
	{name: "END_KEY", tp: mysql.TypeBlob, size: types.UnspecifiedLength},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "DB_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "IS_INDEX", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, deflt: 0},
	{name: "INDEX_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "EPOCH_CONF_VER", tp: mysql.TypeLonglong, size: 21},
	{name: "EPOCH_VERSION", tp: mysql.TypeLonglong, size: 21},
	{name: "WRITTEN_BYTES", tp: mysql.TypeLonglong, size: 21},
	{name: "READ_BYTES", tp: mysql.TypeLonglong, size: 21},
	{name: "APPROXIMATE_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "APPROXIMATE_KEYS", tp: mysql.TypeLonglong, size: 21},
}

// TableTiKVRegionPeersCols is TiKV region peers mem table columns.
var TableTiKVRegionPeersCols = []columnInfo{
	{name: "REGION_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "PEER_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "STORE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "IS_LEARNER", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, deflt: 0},
	{name: "IS_LEADER", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, deflt: 0},
	{name: "STATUS", tp: mysql.TypeVarchar, size: 10, deflt: 0},
	{name: "DOWN_SECONDS", tp: mysql.TypeLonglong, size: 21, deflt: 0},
}

var tableTiDBServersInfoCols = []columnInfo{
	{name: "DDL_ID", tp: mysql.TypeVarchar, size: 64},
	{name: "IP", tp: mysql.TypeVarchar, size: 64},
	{name: "PORT", tp: mysql.TypeLonglong, size: 21},
	{name: "STATUS_PORT", tp: mysql.TypeLonglong, size: 21},
	{name: "LEASE", tp: mysql.TypeVarchar, size: 64},
	{name: "VERSION", tp: mysql.TypeVarchar, size: 64},
	{name: "GIT_HASH", tp: mysql.TypeVarchar, size: 64},
	{name: "BINLOG_STATUS", tp: mysql.TypeVarchar, size: 64},
}

var tableClusterConfigCols = []columnInfo{
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "KEY", tp: mysql.TypeVarchar, size: 256},
	{name: "VALUE", tp: mysql.TypeVarchar, size: 128},
}

var tableClusterLogCols = []columnInfo{
	{name: "TIME", tp: mysql.TypeVarchar, size: 32},
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "LEVEL", tp: mysql.TypeVarchar, size: 8},
	{name: "MESSAGE", tp: mysql.TypeLongBlob, size: types.UnspecifiedLength},
}

var tableClusterLoadCols = []columnInfo{
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "DEVICE_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "DEVICE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "NAME", tp: mysql.TypeVarchar, size: 256},
	{name: "VALUE", tp: mysql.TypeVarchar, size: 128},
}

var tableClusterHardwareCols = []columnInfo{
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "DEVICE_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "DEVICE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "NAME", tp: mysql.TypeVarchar, size: 256},
	{name: "VALUE", tp: mysql.TypeVarchar, size: 128},
}

var tableClusterSystemInfoCols = []columnInfo{
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "SYSTEM_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "SYSTEM_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "NAME", tp: mysql.TypeVarchar, size: 256},
	{name: "VALUE", tp: mysql.TypeVarchar, size: 128},
}

var filesCols = []columnInfo{
	{name: "FILE_ID", tp: mysql.TypeLonglong, size: 4},
	{name: "FILE_NAME", tp: mysql.TypeVarchar, size: 4000},
	{name: "FILE_TYPE", tp: mysql.TypeVarchar, size: 20},
	{name: "TABLESPACE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "LOGFILE_GROUP_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "LOGFILE_GROUP_NUMBER", tp: mysql.TypeLonglong, size: 32},
	{name: "ENGINE", tp: mysql.TypeVarchar, size: 64},
	{name: "FULLTEXT_KEYS", tp: mysql.TypeVarchar, size: 64},
	{name: "DELETED_ROWS", tp: mysql.TypeLonglong, size: 4},
	{name: "UPDATE_COUNT", tp: mysql.TypeLonglong, size: 4},
	{name: "FREE_EXTENTS", tp: mysql.TypeLonglong, size: 4},
	{name: "TOTAL_EXTENTS", tp: mysql.TypeLonglong, size: 4},
	{name: "EXTENT_SIZE", tp: mysql.TypeLonglong, size: 4},
	{name: "INITIAL_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "MAXIMUM_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "AUTOEXTEND_SIZE", tp: mysql.TypeLonglong, size: 21},
	{name: "CREATION_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "LAST_UPDATE_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "LAST_ACCESS_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "RECOVER_TIME", tp: mysql.TypeLonglong, size: 4},
	{name: "TRANSACTION_COUNTER", tp: mysql.TypeLonglong, size: 4},
	{name: "VERSION", tp: mysql.TypeLonglong, size: 21},
	{name: "ROW_FORMAT", tp: mysql.TypeVarchar, size: 10},
	{name: "TABLE_ROWS", tp: mysql.TypeLonglong, size: 21},
	{name: "AVG_ROW_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "MAX_DATA_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "INDEX_LENGTH", tp: mysql.TypeLonglong, size: 21},
	{name: "DATA_FREE", tp: mysql.TypeLonglong, size: 21},
	{name: "CREATE_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "UPDATE_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "CHECK_TIME", tp: mysql.TypeDatetime, size: -1},
	{name: "CHECKSUM", tp: mysql.TypeLonglong, size: 21},
	{name: "STATUS", tp: mysql.TypeVarchar, size: 20},
	{name: "EXTRA", tp: mysql.TypeVarchar, size: 255},
}

var tableClusterInfoCols = []columnInfo{
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "STATUS_ADDRESS", tp: mysql.TypeVarchar, size: 64},
	{name: "VERSION", tp: mysql.TypeVarchar, size: 64},
	{name: "GIT_HASH", tp: mysql.TypeVarchar, size: 64},
	{name: "START_TIME", tp: mysql.TypeVarchar, size: 32},
	{name: "UPTIME", tp: mysql.TypeVarchar, size: 32},
}

var tableTableTiFlashReplicaCols = []columnInfo{
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "REPLICA_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "LOCATION_LABELS", tp: mysql.TypeVarchar, size: 64},
	{name: "AVAILABLE", tp: mysql.TypeTiny, size: 1},
	{name: "PROGRESS", tp: mysql.TypeDouble, size: 22},
}

var tableInspectionResultCols = []columnInfo{
	{name: "RULE", tp: mysql.TypeVarchar, size: 64},
	{name: "ITEM", tp: mysql.TypeVarchar, size: 64},
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "STATUS_ADDRESS", tp: mysql.TypeVarchar, size: 64},
	{name: "VALUE", tp: mysql.TypeVarchar, size: 64},
	{name: "REFERENCE", tp: mysql.TypeVarchar, size: 64},
	{name: "SEVERITY", tp: mysql.TypeVarchar, size: 64},
	{name: "DETAILS", tp: mysql.TypeVarchar, size: 256},
}

var tableInspectionSummaryCols = []columnInfo{
	{name: "RULE", tp: mysql.TypeVarchar, size: 64},
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "METRICS_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "LABEL", tp: mysql.TypeVarchar, size: 64},
	{name: "QUANTILE", tp: mysql.TypeDouble, size: 22},
	{name: "AVG_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MIN_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MAX_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 256},
}

var tableInspectionRulesCols = []columnInfo{
	{name: "NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 256},
}

var tableMetricTablesCols = []columnInfo{
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "PROMQL", tp: mysql.TypeVarchar, size: 64},
	{name: "LABELS", tp: mysql.TypeVarchar, size: 64},
	{name: "QUANTILE", tp: mysql.TypeDouble, size: 22},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 256},
}

var tableMetricSummaryCols = []columnInfo{
	{name: "METRICS_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "QUANTILE", tp: mysql.TypeDouble, size: 22},
	{name: "SUM_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "AVG_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MIN_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MAX_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 256},
}

var tableMetricSummaryByLabelCols = []columnInfo{
	{name: "INSTANCE", tp: mysql.TypeVarchar, size: 64},
	{name: "METRICS_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "LABEL", tp: mysql.TypeVarchar, size: 64},
	{name: "QUANTILE", tp: mysql.TypeDouble, size: 22},
	{name: "SUM_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "AVG_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MIN_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "MAX_VALUE", tp: mysql.TypeDouble, size: 22, decimal: 6},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 256},
}

var tableDDLJobsCols = []columnInfo{
	{name: "JOB_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "DB_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "JOB_TYPE", tp: mysql.TypeVarchar, size: 64},
	{name: "SCHEMA_STATE", tp: mysql.TypeVarchar, size: 64},
	{name: "SCHEMA_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "ROW_COUNT", tp: mysql.TypeLonglong, size: 21},
	{name: "START_TIME", tp: mysql.TypeDatetime, size: 19},
	{name: "END_TIME", tp: mysql.TypeDatetime, size: 19},
	{name: "STATE", tp: mysql.TypeVarchar, size: 64},
	{name: "QUERY", tp: mysql.TypeVarchar, size: 64},
}

var tableSequencesCols = []columnInfo{
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "SEQUENCE_SCHEMA", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "SEQUENCE_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "CACHE", tp: mysql.TypeTiny, flag: mysql.NotNullFlag},
	{name: "CACHE_VALUE", tp: mysql.TypeLonglong, size: 21},
	{name: "CYCLE", tp: mysql.TypeTiny, flag: mysql.NotNullFlag},
	{name: "INCREMENT", tp: mysql.TypeLonglong, size: 21, flag: mysql.NotNullFlag},
	{name: "MAX_VALUE", tp: mysql.TypeLonglong, size: 21},
	{name: "MIN_VALUE", tp: mysql.TypeLonglong, size: 21},
	{name: "START", tp: mysql.TypeLonglong, size: 21},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 64},
}

// pgTableInformationSchemaCatalogName is table information_schema_catalog_name columns
// https://www.postgresql.org/docs/13/infoschema-information-schema-catalog-name.html
var pgTableInformationSchemaCatalogNameCols = []columnInfo{
	{name: "catalog_name", tp: mysql.TypeVarchar, size: 32},
}

// pgTableAdministrableRoleAuthorizationsCols is table administrable_role_authorizations columns
// https://www.postgresql.org/docs/13/infoschema-administrable-role-authorizations.html
var pgTableAdministrableRoleAuthorizationsCols = []columnInfo{
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "role_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 32},
}

// pgTableApplicableRolesCols is table application_roles columns
// https://www.postgresql.org/docs/13/infoschema-applicable-roles.html
var pgTableApplicableRolesCols = []columnInfo{
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "role_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 32},
}

// pgTableAttributesCols is table attribute columns
// https://www.postgresql.org/docs/13/infoschema-attributes.html
var pgTableAttributesCols = []columnInfo{
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "attribute_name", tp: mysql.TypeVarchar, size: 64},
	{name: "ordinal_position", tp: mysql.TypeVarchar, size: 64},
	{name: "attribute_default", tp: mysql.TypeVarchar, size: 64},
	{name: "is_nullable", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_octet_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "attribute_udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "attribute_udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "attribute_udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "is_derived_reference_attribute", tp: mysql.TypeVarchar, size: 64},
}

// pgTableCharacterSetsCols is table character_sets columns
// https://www.postgresql.org/docs/13/infoschema-character-sets.html
var pgTableCharacterSetsCols = []columnInfo{
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 32},
	{name: "character_repertoire", tp: mysql.TypeVarchar, size: 64},
	{name: "form_of_use", tp: mysql.TypeVarchar, size: 64},
	{name: "default_collate_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "default_collate_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "default_collate_name", tp: mysql.TypeVarchar, size: 32},
	{name: "DESCRIPTION", tp: mysql.TypeVarchar, size: 60, comment: "TiDB table cols"},
	{name: "MAXLEN", tp: mysql.TypeLonglong, size: 3, comment: "TiDB table cols"},
}

// pgTableCheckConstraintRoutineUsageCols is table check_constraint_routine_usage columns
// https://www.postgresql.org/docs/13/infoschema-check-constraint-routine-usage.html
var pgTableCheckConstraintRoutineUsageCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableCheckConstraintsCols is table check_constraints columns
// https://www.postgresql.org/docs/13/infoschema-check-constraints.html
var pgTableCheckConstraintsCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 128},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "check_clause", tp: mysql.TypeVarchar, size: 64},
}

// pgTableCollationsCols is table collations columns
// https://www.postgresql.org/docs/13/infoschema-collations.html
var pgTableCollationsCols = []columnInfo{
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "pad_attribute", tp: mysql.TypeVarchar, size: 64},
	{name: "CHARACTER_SET_NAME", tp: mysql.TypeVarchar, size: 32, comment: "TiDB table cols"},
	{name: "ID", tp: mysql.TypeLonglong, size: 11, comment: "TiDB table cols"},
	{name: "IS_DEFAULT", tp: mysql.TypeVarchar, size: 3, comment: "TiDB table cols"},
	{name: "IS_COMPILED", tp: mysql.TypeVarchar, size: 3, comment: "TiDB table cols"},
	{name: "SORTLEN", tp: mysql.TypeLonglong, size: 3, comment: "TiDB table cols"},
}

// pgTableCollationCharacterSetApplicabilityCols is table collation_character_set_applicability columns
// https://www.postgresql.org/docs/13/infoschema-collation-character-set-applicab.html
var pgTableCollationCharacterSetApplicabilityCols = []columnInfo{
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag},
}

// pgTableColumnColumnUsageCols is table column_column_usage columns
// https://www.postgresql.org/docs/13/infoschema-column-column-usage.html
var pgTableColumnColumnUsageCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "dependent_column", tp: mysql.TypeVarchar, size: 64},
}

// pgTableColumnDomainUsageCols is table column_domain_usage columns
// https://www.postgresql.org/docs/13/infoschema-column-domain-usage.html
var pgTableColumnDomainUsageCols = []columnInfo{
	{name: "domain_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableColumnOptionsCols is table column_options columns
// https://www.postgresql.org/docs/13/infoschema-column-options.html
var pgTableColumnOptionsCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_value", tp: mysql.TypeVarchar, size: 64},
}

// pgTableColumnPrivilegesCols is table column_privileges columns
// https://www.postgresql.org/docs/13/infoschema-column-privileges.html
var pgTableColumnPrivilegesCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableColumnUdtUsageCols is table column_udt_usage columns
// https://www.postgresql.org/docs/13/infoschema-column-udt-usage.html
var pgTableColumnUdtUsageCols = []columnInfo{
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableColumnsCols is table columns columns
// https://www.postgresql.org/docs/13/infoschema-columns.html
var pgTableColumnsCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 512},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "ordinal_position", tp: mysql.TypeLonglong, size: 64},
	{name: "column_default", tp: mysql.TypeBlob, size: 196606},
	{name: "is_nullable", tp: mysql.TypeVarchar, size: 3},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeLonglong, size: 21},
	{name: "character_octet_length", tp: mysql.TypeLonglong, size: 21},
	{name: "numeric_precision", tp: mysql.TypeLonglong, size: 21},
	{name: "numeric_precision_radix", tp: mysql.TypeLonglong, size: 21},
	{name: "numeric_scale", tp: mysql.TypeLonglong, size: 21},
	{name: "datetime_precision", tp: mysql.TypeLonglong, size: 21},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeInt24, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 32},
	{name: "domain_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_name", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeInt24, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "is_self_referencing", tp: mysql.TypeVarchar, size: 64},
	{name: "is_identity", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_generation", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_start", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_increment", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_maximum", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_minimum", tp: mysql.TypeVarchar, size: 64},
	{name: "identity_cycle", tp: mysql.TypeVarchar, size: 64},
	{name: "is_generated", tp: mysql.TypeVarchar, size: 64},
	{name: "generation_expression", tp: mysql.TypeBlob, size: 589779, flag: mysql.NotNullFlag},
	{name: "is_updatable", tp: mysql.TypeVarchar, size: 64},
	{name: "COLUMN_TYPE", tp: mysql.TypeBlob, size: 196606, comment: "TiDB Table Cols"},
	{name: "COLUMN_KEY", tp: mysql.TypeVarchar, size: 3, comment: "TiDB Table Cols"},
	{name: "EXTRA", tp: mysql.TypeVarchar, size: 30, comment: "TiDB Table Cols"},
	{name: "PRIVILEGES", tp: mysql.TypeVarchar, size: 80, comment: "TiDB Table Cols"},
	{name: "COLUMN_COMMENT", tp: mysql.TypeVarchar, size: 1024, comment: "TiDB Table Cols"},
}

// pgTableConstraintColumnUsageCols is table constraint_column_usage columns
// https://www.postgresql.org/docs/13/infoschema-constraint-column-usage.html
var pgTableConstraintColumnUsageCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableConstraintTableUsageCols is table constraint_tale_usage columns
// https://www.postgresql.org/docs/13/infoschema-constraint-table-usage.html
var pgTableConstraintTableUsageCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableDataTypePrivilegesCols is table data_type_privileges columns
// https://www.postgresql.org/docs/13/infoschema-data-type-privileges.html
var pgTableDataTypePrivilegesCols = []columnInfo{
	{name: "object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "object_type", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableDomainConstraintsCols is table domain_constraints columns
// https://www.postgresql.org/docs/13/infoschema-domain-constraints.html
var pgTableDomainConstraintsCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_deferrable", tp: mysql.TypeVarchar, size: 64},
	{name: "initially_deferred", tp: mysql.TypeVarchar, size: 64},
}

// pgTableDomainUdtUsageCols is table domain_udt_usage columns
// https://www.postgresql.org/docs/13/infoschema-domain-udt-usage.html
var pgTableDomainUdtUsageCols = []columnInfo{
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableDomainsCols is table domain columns
// https://www.postgresql.org/docs/13/infoschema-domains.html
var pgTableDomainsCols = []columnInfo{
	{name: "domain_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_name", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeInt24, size: 64},
	{name: "character_octet_length", tp: mysql.TypeInt24, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_default", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableElementTypesCols is table element_types columns
// https://www.postgresql.org/docs/13/infoschema-element-types.html
var pgTableElementTypesCols = []columnInfo{
	{name: "object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "object_type", tp: mysql.TypeVarchar, size: 64},
	{name: "collection_type_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeInt24, size: 64},
	{name: "character_octet_length", tp: mysql.TypeInt24, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "domain_default", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableEnabledRolesCols is table enabled_roles columns
// https://www.postgresql.org/docs/13/infoschema-enabled-roles.html
var pgTableEnabledRolesCols = []columnInfo{
	{name: "role_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignDataWrapperOptionsCols is table foreign_data_wrapper_options columns
// https://www.postgresql.org/docs/13/infoschema-foreign-data-wrapper-options.html
var pgTableForeignDataWrapperOptionsCols = []columnInfo{
	{name: "foreign_data_wrapper_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_data_wrapper_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_value", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignDataWrappersCols is table foreign_data_wrapper columns
// https://www.postgresql.org/docs/13/infoschema-foreign-data-wrappers.html
var pgTableForeignDataWrappersCols = []columnInfo{
	{name: "foreign_data_wrapper_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_data_wrapper_name", tp: mysql.TypeVarchar, size: 64},
	{name: "authorization_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "library_name", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_data_wrapper_language", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignServerOptionsCols is table foreign_server_options columns
// https://www.postgresql.org/docs/13/infoschema-foreign-server-options.html
var pgTableForeignServerOptionsCols = []columnInfo{
	{name: "foreign_server_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_value", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignServers is table foreign_servers columns
// https://www.postgresql.org/docs/13/infoschema-foreign-servers.html
var pgTableForeignServers = []columnInfo{
	{name: "foreign_server_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_name", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_data_wrapper_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_data_wrapper_name", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_type", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_version", tp: mysql.TypeVarchar, size: 64},
	{name: "authorization_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignTableOptionsCols is table foreign_table_options columns
// https://www.postgresql.org/docs/13/infoschema-foreign-table-options.html
var pgTableForeignTableOptionsCols = []columnInfo{
	{name: "foreign_table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_value", tp: mysql.TypeVarchar, size: 64},
}

// pgTableForeignTablesCols is table foreign_tables columns
// https://www.postgresql.org/docs/13/infoschema-foreign-tables.html
var pgTableForeignTablesCols = []columnInfo{
	{name: "foreign_table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableKeyColumnUsageCols is table key_column_usage columns
// https://www.postgresql.org/docs/13/infoschema-key-column-usage.html
var pgTableKeyColumnUsageCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "ordinal_position", tp: mysql.TypeLonglong, size: 10, flag: mysql.NotNullFlag},
	{name: "position_in_unique_constraint", tp: mysql.TypeLonglong, size: 10},
	{name: "REFERENCED_TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "REFERENCED_TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "REFERENCED_COLUMN_NAME", tp: mysql.TypeVarchar, size: 64},
}

// pgTableParametersCols is table parameters columns
// https://www.postgresql.org/docs/13/infoschema-parameters.html
var pgTableParametersCols = []columnInfo{
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "ordinal_position", tp: mysql.TypeVarchar, size: 64},
	{name: "parameter_mode", tp: mysql.TypeVarchar, size: 64},
	{name: "is_result", tp: mysql.TypeVarchar, size: 64},
	{name: "as_local", tp: mysql.TypeVarchar, size: 64},
	{name: "parameter_name", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_octet_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "parameter_default", tp: mysql.TypeVarchar, size: 64},
}

// pgTableReferentialConstraintsCols is table referential_constraints columns
// https://www.postgresql.org/docs/13/infoschema-referential-constraints.html
var pgTableReferentialConstraintsCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "unique_constraint_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "unique_constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "unique_constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "match_option", tp: mysql.TypeVarchar, size: 64},
	{name: "update_rule", tp: mysql.TypeVarchar, size: 64},
	{name: "delete_rule", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoleColumnGrantsCols is table role_column_grants columns
// https://www.postgresql.org/docs/13/infoschema-role-column-grants.html
var pgTableRoleColumnGrantsCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoleRoutineGrantsCols is table role_routine_grants columns
// https://www.postgresql.org/docs/13/infoschema-role-routine-grants.html
var pgTableRoleRoutineGrantsCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoleTableGrantsCols is table role_table_grants columns
// https://www.postgresql.org/docs/13/infoschema-role-table-grants.html
var pgTableRoleTableGrantsCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
	{name: "with_hierarchy", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoleUdtGrantsCols is table role_udt_grants columns
// https://www.postgresql.org/docs/13/infoschema-role-udt-grants.html
var pgTableRoleUdtGrantsCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoleUsageGrantsCols is table role_usage_grants columns
// https://www.postgresql.org/docs/13/infoschema-role-usage-grants.html
var pgTableRoleUsageGrantsCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "object_type", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoutinePrivilegesCols is table routine_privileges columns
// https://www.postgresql.org/docs/13/infoschema-routine-privileges.html
var pgTableRoutinePrivilegesCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableRoutinesCols is table routines columns
// https://www.postgresql.org/docs/13/infoschema-routines.html
var pgTableRoutinesCols = []columnInfo{
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_name", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_type", tp: mysql.TypeVarchar, size: 64},
	{name: "module_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "module_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "module_name", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_octet_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "type_udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "type_udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "type_udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "dtd_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_body", tp: mysql.TypeVarchar, size: 64},
	{name: "routine_definition", tp: mysql.TypeVarchar, size: 64},
	{name: "external_name", tp: mysql.TypeVarchar, size: 64},
	{name: "external_language", tp: mysql.TypeVarchar, size: 64},
	{name: "parameter_style", tp: mysql.TypeVarchar, size: 64},
	{name: "is_deterministic", tp: mysql.TypeVarchar, size: 64},
	{name: "sql_data_access", tp: mysql.TypeVarchar, size: 64},
	{name: "is_null_call", tp: mysql.TypeVarchar, size: 64},
	{name: "sql_path", tp: mysql.TypeVarchar, size: 64},
	{name: "schema_level_routine", tp: mysql.TypeVarchar, size: 64},
	{name: "max_dynamic_result_sets", tp: mysql.TypeVarchar, size: 64},
	{name: "is_user_defined_cast", tp: mysql.TypeVarchar, size: 64},
	{name: "is_implicitly_invocable", tp: mysql.TypeVarchar, size: 64},
	{name: "security_type", tp: mysql.TypeVarchar, size: 64},
	{name: "to_sql_specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "to_sql_specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "to_sql_specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "as_locator", tp: mysql.TypeVarchar, size: 64},
	{name: "created", tp: mysql.TypeVarchar, size: 64},
	{name: "last_altered", tp: mysql.TypeVarchar, size: 64},
	{name: "new_savepoint_level", tp: mysql.TypeVarchar, size: 64},
	{name: "id_udt_dependent", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_from_data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_as_locator", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_char_max_length", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_char_octet_length", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_char_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_char_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_char_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_type_udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_type_udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_type_udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_scope_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_scope_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_scope_name", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_maximum_cardinality", tp: mysql.TypeVarchar, size: 64},
	{name: "result_cast_dtd_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableSchemataCols is table schemata columns
// https://www.postgresql.org/docs/13/infoschema-schemata.html
var pgTableSchemataCols = []columnInfo{
	{name: "catalog_name", tp: mysql.TypeVarchar, size: 512},
	{name: "schema_name", tp: mysql.TypeVarchar, size: 64},
	{name: "schema_owner", tp: mysql.TypeVarchar, size: 64},
	{name: "default_character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "default_character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "default_character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "sql_path", tp: mysql.TypeVarchar, size: 512},
	{name: "DEFAULT_COLLATION_NAME", tp: mysql.TypeVarchar, size: 32, comment: "TiDB Schemata col"},
}

// pgTableSequencesCols is table sequences columns
// https://www.postgresql.org/docs/13/infoschema-sequences.html
var pgTableSequencesCols = []columnInfo{
	{name: "sequence_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "sequence_schema", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "sequence_name", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 32},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "start_value", tp: mysql.TypeVarchar, size: 64},
	{name: "minimum_value", tp: mysql.TypeVarchar, size: 64},
	{name: "maximum_value", tp: mysql.TypeVarchar, size: 64},
	{name: "increment", tp: mysql.TypeLonglong, size: 21, flag: mysql.NotNullFlag},
	{name: "cycle_option", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_CATALOG", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "CACHE", tp: mysql.TypeTiny, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "CACHE_VALUE", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "CYCLE", tp: mysql.TypeTiny, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "MAX_VALUE", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "MIN_VALUE", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "START", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "COMMENT", tp: mysql.TypeVarchar, size: 64, comment: "TiDB Table Cols"},
}

// pgTableSQLFeaturesCols is table sql_features columns
// https://www.postgresql.org/docs/13/infoschema-sql-features.html
var pgTableSQLFeaturesCols = []columnInfo{
	{name: "feature_id", tp: mysql.TypeVarchar, size: 64},
	{name: "feature_name", tp: mysql.TypeVarchar, size: 64},
	{name: "sub_feature_id", tp: mysql.TypeVarchar, size: 64},
	{name: "sub_feature_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_supported", tp: mysql.TypeVarchar, size: 64},
	{name: "is_verified_by", tp: mysql.TypeVarchar, size: 32},
	{name: "comments", tp: mysql.TypeVarchar, size: 64},
}

// pgTableSQLImplementationInfoCols is table sql_implementation_info columns
// https://www.postgresql.org/docs/13/infoschema-sql-implementation-info.html
var pgTableSQLImplementationInfoCols = []columnInfo{
	{name: "implementation_info_id", tp: mysql.TypeVarchar, size: 64},
	{name: "implementation_info_name", tp: mysql.TypeVarchar, size: 64},
	{name: "integer_value", tp: mysql.TypeVarchar, size: 64},
	{name: "character_value", tp: mysql.TypeVarchar, size: 64},
	{name: "comments", tp: mysql.TypeVarchar, size: 64},
}

// pgTableSQLPartsCols is table sql_parts columns
// https://www.postgresql.org/docs/13/infoschema-sql-parts.html
var pgTableSQLPartsCols = []columnInfo{
	{name: "feature_id", tp: mysql.TypeVarchar, size: 64},
	{name: "feature_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_supported", tp: mysql.TypeVarchar, size: 64},
	{name: "is_verified_by", tp: mysql.TypeVarchar, size: 64},
	{name: "comments", tp: mysql.TypeVarchar, size: 64},
}

// pgTableSqlSizing is table sql_sizing columns
// https://www.postgresql.org/docs/13/infoschema-sql-sizing.html
var pgTableSQLSizingCols = []columnInfo{
	{name: "sizing_id", tp: mysql.TypeInt24, size: 64},
	{name: "sizing_name", tp: mysql.TypeVarchar, size: 64},
	{name: "supported_value", tp: mysql.TypeInt24, size: 64},
	{name: "comments", tp: mysql.TypeVarchar, size: 64},
}

// pgTableTableConstraintsCols is table table_constraints columns
// https://www.postgresql.org/docs/13/infoschema-table-constraints.html
var pgTableTableConstraintsCols = []columnInfo{
	{name: "constraint_catalog", tp: mysql.TypeVarchar, size: 512},
	{name: "constraint_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "constraint_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_deferrable", tp: mysql.TypeVarchar, size: 64},
	{name: "initially_deferred", tp: mysql.TypeVarchar, size: 64},
	{name: "enforced", tp: mysql.TypeVarchar, size: 64},
}

// pgTableTablePrivilegesCols is table table_privileges columns
// https://www.postgresql.org/docs/13/infoschema-table-privileges.html
var pgTableTablePrivilegesCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
	{name: "with_hierarchy", tp: mysql.TypeVarchar, size: 64},
}

// pgTableTablesCols is table tables columns
// https://www.postgresql.org/docs/13/infoschema-tables.html
var pgTableTablesCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_type", tp: mysql.TypeVarchar, size: 64},
	{name: "self_referencing_column_name", tp: mysql.TypeVarchar, size: 64},
	{name: "reference_generation", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_name", tp: mysql.TypeVarchar, size: 64},
	{name: "is_insertable_into", tp: mysql.TypeVarchar, size: 64},
	{name: "is_typed", tp: mysql.TypeVarchar, size: 64},
	{name: "commit_action", tp: mysql.TypeVarchar, size: 64},
	{name: "ENGINE", tp: mysql.TypeVarchar, size: 64, comment: "TiDB Table Cols"},
	{name: "VERSION", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "ROW_FORMAT", tp: mysql.TypeVarchar, size: 10, comment: "TiDB Table Cols"},
	{name: "TABLE_ROWS", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "AVG_ROW_LENGTH", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "DATA_LENGTH", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "MAX_DATA_LENGTH", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "INDEX_LENGTH", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "DATA_FREE", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "AUTO_INCREMENT", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "CREATE_TIME", tp: mysql.TypeDatetime, size: 19, comment: "TiDB Table Cols"},
	{name: "UPDATE_TIME", tp: mysql.TypeDatetime, size: 19, comment: "TiDB Table Cols"},
	{name: "CHECK_TIME", tp: mysql.TypeDatetime, size: 19, comment: "TiDB Table Cols"},
	{name: "TABLE_COLLATION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag, deflt: "utf8_bin", comment: "TiDB Table Cols"},
	{name: "CHECKSUM", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "CREATE_OPTIONS", tp: mysql.TypeVarchar, size: 255, comment: "TiDB Table Cols"},
	{name: "TABLE_COMMENT", tp: mysql.TypeVarchar, size: 2048, comment: "TiDB Table Cols"},
	{name: "TIDB_TABLE_ID", tp: mysql.TypeLonglong, size: 21, comment: "TiDB Table Cols"},
	{name: "TIDB_ROW_ID_SHARDING_INFO", tp: mysql.TypeVarchar, size: 255, comment: "TiDB Table Cols"},
}

// pgTableTransformsCols is table transforms columns
// https://www.postgresql.org/docs/13/infoschema-transforms.html
var pgTableTransformsCols = []columnInfo{
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
	{name: "group_name", tp: mysql.TypeVarchar, size: 64},
	{name: "transform_type", tp: mysql.TypeVarchar, size: 64},
}

// pgTableTriggeredUpdateColumns is table triggered_update_columns columns
// https://www.postgresql.org/docs/13/infoschema-triggered-update-columns.html
var pgTableTriggeredUpdateColumns = []columnInfo{
	{name: "trigger_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "trigger_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "trigger_name", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_column", tp: mysql.TypeVarchar, size: 64},
}

// pgTableTriggersCols is table triggers columns
// https://www.postgresql.org/docs/13/infoschema-triggers.html
var pgTableTriggersCols = []columnInfo{
	{name: "trigger_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "trigger_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "trigger_name", tp: mysql.TypeVarchar, size: 64},
	{name: "event_manipulation", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "event_object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "action_order", tp: mysql.TypeVarchar, size: 64},
	{name: "action_condition", tp: mysql.TypeVarchar, size: 64},
	{name: "action_statement", tp: mysql.TypeVarchar, size: 64},
	{name: "action_orientation", tp: mysql.TypeVarchar, size: 64},
	{name: "action_timing", tp: mysql.TypeVarchar, size: 64},
	{name: "action_reference_old_table", tp: mysql.TypeVarchar, size: 64},
	{name: "action_reference_new_table", tp: mysql.TypeVarchar, size: 64},
	{name: "action_reference_old_row", tp: mysql.TypeVarchar, size: 64},
	{name: "action_reference_new_row", tp: mysql.TypeVarchar, size: 64},
	{name: "created", tp: mysql.TypeVarchar, size: 64},
}

// pgTableUdtPrivilegesCols is table udt_privileges columns
// https://www.postgresql.org/docs/13/infoschema-udt-privileges.html
var pgTableUdtPrivilegesCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "udt_name", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableUsagePrivilegesCols is table usage_privileges columns
// https://www.postgresql.org/docs/13/infoschema-usage-privileges.html
var pgTableUsagePrivilegesCols = []columnInfo{
	{name: "grantor", tp: mysql.TypeVarchar, size: 64},
	{name: "grantee", tp: mysql.TypeVarchar, size: 64},
	{name: "object_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "object_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "object_name", tp: mysql.TypeVarchar, size: 64},
	{name: "object_type", tp: mysql.TypeVarchar, size: 64},
	{name: "privilege_type", tp: mysql.TypeVarchar, size: 64},
	{name: "is_grantable", tp: mysql.TypeVarchar, size: 64},
}

// pgTableUserDefinedTypesCols is table user_defined_types columns
// https://www.postgresql.org/docs/13/infoschema-user-defined-types.html
var pgTableUserDefinedTypesCols = []columnInfo{
	{name: "user_defined_type_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_name", tp: mysql.TypeVarchar, size: 64},
	{name: "user_defined_type_category", tp: mysql.TypeVarchar, size: 64},
	{name: "is_instantiable", tp: mysql.TypeVarchar, size: 64},
	{name: "is_final", tp: mysql.TypeVarchar, size: 64},
	{name: "ordering_form", tp: mysql.TypeVarchar, size: 64},
	{name: "ordering_category", tp: mysql.TypeVarchar, size: 64},
	{name: "ordering_routine_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "ordering_routine_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "ordering_routine_name", tp: mysql.TypeVarchar, size: 64},
	{name: "reference_type", tp: mysql.TypeVarchar, size: 64},
	{name: "data_type", tp: mysql.TypeVarchar, size: 64},
	{name: "character_maximum_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_octet_length", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "character_set_name", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "collation_name", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_precision_radix", tp: mysql.TypeVarchar, size: 64},
	{name: "numeric_scale", tp: mysql.TypeVarchar, size: 64},
	{name: "datetime_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_type", tp: mysql.TypeVarchar, size: 64},
	{name: "interval_precision", tp: mysql.TypeVarchar, size: 64},
	{name: "source_dtd_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "ref_dtd_identifier", tp: mysql.TypeVarchar, size: 64},
}

// pgTableUserMappingOptionsCols is table user_mapping_options columns
// https://www.postgresql.org/docs/13/infoschema-user-mapping-options.html
var pgTableUserMappingOptionsCols = []columnInfo{
	{name: "authorization_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_name", tp: mysql.TypeVarchar, size: 64},
	{name: "option_value", tp: mysql.TypeVarchar, size: 64},
}

// pgTableUserMappingsCols is table user_mappings columns
// https://www.postgresql.org/docs/13/infoschema-user-mappings.html
var pgTableUserMappingsCols = []columnInfo{
	{name: "authorization_identifier", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "foreign_server_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableViewColumnUsageCols is table view_column_usage columns
// https://www.postgresql.org/docs/13/infoschema-view-column-usage.html
var pgTableViewColumnUsageCols = []columnInfo{
	{name: "view_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "view_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "view_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "column_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableViewRoutineUsageCols is table view_routine_usage
// https://www.postgresql.org/docs/13/infoschema-view-routine-usage.html
var pgTableViewRoutineUsageCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "specific_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableViewTableUsageCols is table view_table_usage columns
// https://www.postgresql.org/docs/13/infoschema-view-table-usage.html
var pgTableViewTableUsageCols = []columnInfo{
	{name: "view_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "view_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "view_name", tp: mysql.TypeVarchar, size: 64},
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 64},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64},
}

// pgTableViewsCols is table views columns
// https://www.postgresql.org/docs/13/infoschema-views.html
var pgTableViewsCols = []columnInfo{
	{name: "table_catalog", tp: mysql.TypeVarchar, size: 512, flag: mysql.NotNullFlag},
	{name: "table_schema", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "table_name", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "view_definition", tp: mysql.TypeLongBlob, flag: mysql.NotNullFlag},
	{name: "check_option", tp: mysql.TypeVarchar, size: 8, flag: mysql.NotNullFlag},
	{name: "is_updatable", tp: mysql.TypeVarchar, size: 3, flag: mysql.NotNullFlag},
	{name: "is_insertable_into", tp: mysql.TypeVarchar, size: 64},
	{name: "is_trigger_updatable", tp: mysql.TypeVarchar, size: 64},
	{name: "is_trigger_deletable", tp: mysql.TypeVarchar, size: 64},
	{name: "is_trigger_insertable_into", tp: mysql.TypeVarchar, size: 64},
	{name: "DEFINER", tp: mysql.TypeVarchar, size: 77, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "SECURITY_TYPE", tp: mysql.TypeVarchar, size: 7, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "CHARACTER_SET_CLIENT", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
	{name: "COLLATION_CONNECTION", tp: mysql.TypeVarchar, size: 32, flag: mysql.NotNullFlag, comment: "TiDB Table Cols"},
}

func dataForTiKVRegionStatus(ctx sessionctx.Context) (records [][]types.Datum, err error) {
	tikvStore, ok := ctx.GetStore().(tikv.Storage)
	if !ok {
		return nil, errors.New("Information about TiKV region status can be gotten only when the storage is TiKV")
	}
	tikvHelper := &helper.Helper{
		Store:       tikvStore,
		RegionCache: tikvStore.GetRegionCache(),
	}
	regionsInfo, err := tikvHelper.GetRegionsInfo()
	if err != nil {
		return nil, err
	}
	allSchemas := ctx.GetSessionVars().TxnCtx.InfoSchema.(InfoSchema).AllSchemas()
	tableInfos := tikvHelper.GetRegionsTableInfo(regionsInfo, allSchemas)
	for _, region := range regionsInfo.Regions {
		tableList := tableInfos[region.ID]
		if len(tableList) == 0 {
			records = append(records, newTiKVRegionStatusCol(&region, nil))
		}
		for _, table := range tableList {
			row := newTiKVRegionStatusCol(&region, &table)
			records = append(records, row)
		}
	}
	return records, nil
}

func newTiKVRegionStatusCol(region *helper.RegionInfo, table *helper.TableInfo) []types.Datum {
	row := make([]types.Datum, len(tableTiKVRegionStatusCols))
	row[0].SetInt64(region.ID)
	row[1].SetString(region.StartKey, mysql.DefaultCollationName)
	row[2].SetString(region.EndKey, mysql.DefaultCollationName)
	if table != nil {
		row[3].SetInt64(table.Table.ID)
		row[4].SetString(table.DB.Name.O, mysql.DefaultCollationName)
		row[5].SetString(table.Table.Name.O, mysql.DefaultCollationName)
		if table.IsIndex {
			row[6].SetInt64(1)
			row[7].SetInt64(table.Index.ID)
			row[8].SetString(table.Index.Name.O, mysql.DefaultCollationName)
		} else {
			row[6].SetInt64(0)
		}
	}
	row[9].SetInt64(region.Epoch.ConfVer)
	row[10].SetInt64(region.Epoch.Version)
	row[11].SetInt64(region.WrittenBytes)
	row[12].SetInt64(region.ReadBytes)
	row[13].SetInt64(region.ApproximateSize)
	row[14].SetInt64(region.ApproximateKeys)
	return row
}

func dataForTiKVStoreStatus(ctx sessionctx.Context) (records [][]types.Datum, err error) {
	tikvStore, ok := ctx.GetStore().(tikv.Storage)
	if !ok {
		return nil, errors.New("Information about TiKV store status can be gotten only when the storage is TiKV")
	}
	tikvHelper := &helper.Helper{
		Store:       tikvStore,
		RegionCache: tikvStore.GetRegionCache(),
	}
	storesStat, err := tikvHelper.GetStoresStat()
	if err != nil {
		return nil, err
	}
	for _, storeStat := range storesStat.Stores {
		row := make([]types.Datum, len(tableTiKVStoreStatusCols))
		row[0].SetInt64(storeStat.Store.ID)
		row[1].SetString(storeStat.Store.Address, mysql.DefaultCollationName)
		row[2].SetInt64(storeStat.Store.State)
		row[3].SetString(storeStat.Store.StateName, mysql.DefaultCollationName)
		data, err := json.Marshal(storeStat.Store.Labels)
		if err != nil {
			return nil, err
		}
		bj := binaryJson.BinaryJSON{}
		if err = bj.UnmarshalJSON(data); err != nil {
			return nil, err
		}
		row[4].SetMysqlJSON(bj)
		row[5].SetString(storeStat.Store.Version, mysql.DefaultCollationName)
		row[6].SetString(storeStat.Status.Capacity, mysql.DefaultCollationName)
		row[7].SetString(storeStat.Status.Available, mysql.DefaultCollationName)
		row[8].SetInt64(storeStat.Status.LeaderCount)
		row[9].SetFloat64(storeStat.Status.LeaderWeight)
		row[10].SetFloat64(storeStat.Status.LeaderScore)
		row[11].SetInt64(storeStat.Status.LeaderSize)
		row[12].SetInt64(storeStat.Status.RegionCount)
		row[13].SetFloat64(storeStat.Status.RegionWeight)
		row[14].SetFloat64(storeStat.Status.RegionScore)
		row[15].SetInt64(storeStat.Status.RegionSize)
		startTs := types.NewTime(types.FromGoTime(storeStat.Status.StartTs), mysql.TypeDatetime, types.DefaultFsp)
		row[16].SetMysqlTime(startTs)
		lastHeartbeatTs := types.NewTime(types.FromGoTime(storeStat.Status.LastHeartbeatTs), mysql.TypeDatetime, types.DefaultFsp)
		row[17].SetMysqlTime(lastHeartbeatTs)
		row[18].SetString(storeStat.Status.Uptime, mysql.DefaultCollationName)
		records = append(records, row)
	}
	return records, nil
}

var tableStatementsSummaryCols = []columnInfo{
	{name: "SUMMARY_BEGIN_TIME", tp: mysql.TypeTimestamp, size: 26, flag: mysql.NotNullFlag, comment: "Begin time of this summary"},
	{name: "SUMMARY_END_TIME", tp: mysql.TypeTimestamp, size: 26, flag: mysql.NotNullFlag, comment: "End time of this summary"},
	{name: "STMT_TYPE", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, comment: "Statement type"},
	{name: "SCHEMA_NAME", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag, comment: "Current schema"},
	{name: "DIGEST", tp: mysql.TypeVarchar, size: 64, flag: mysql.NotNullFlag},
	{name: "DIGEST_TEXT", tp: mysql.TypeBlob, size: types.UnspecifiedLength, flag: mysql.NotNullFlag, comment: "Normalized statement"},
	{name: "TABLE_NAMES", tp: mysql.TypeBlob, size: types.UnspecifiedLength, comment: "Involved tables"},
	{name: "INDEX_NAMES", tp: mysql.TypeBlob, size: types.UnspecifiedLength, comment: "Used indices"},
	{name: "SAMPLE_USER", tp: mysql.TypeVarchar, size: 64, comment: "Sampled user who executed these statements"},
	{name: "EXEC_COUNT", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Count of executions"},
	{name: "SUM_ERRORS", tp: mysql.TypeLong, size: 11, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum of errors"},
	{name: "SUM_WARNINGS", tp: mysql.TypeLong, size: 11, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum of warnings"},
	{name: "SUM_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum latency of these statements"},
	{name: "MAX_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max latency of these statements"},
	{name: "MIN_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Min latency of these statements"},
	{name: "AVG_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average latency of these statements"},
	{name: "AVG_PARSE_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average latency of parsing"},
	{name: "MAX_PARSE_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max latency of parsing"},
	{name: "AVG_COMPILE_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average latency of compiling"},
	{name: "MAX_COMPILE_LATENCY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max latency of compiling"},
	{name: "SUM_COP_TASK_NUM", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Total number of CopTasks"},
	{name: "MAX_COP_PROCESS_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max processing time of CopTasks"},
	{name: "MAX_COP_PROCESS_ADDRESS", tp: mysql.TypeVarchar, size: 256, comment: "Address of the CopTask with max processing time"},
	{name: "MAX_COP_WAIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max waiting time of CopTasks"},
	{name: "MAX_COP_WAIT_ADDRESS", tp: mysql.TypeVarchar, size: 256, comment: "Address of the CopTask with max waiting time"},
	{name: "AVG_PROCESS_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average processing time in TiKV"},
	{name: "MAX_PROCESS_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max processing time in TiKV"},
	{name: "AVG_WAIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average waiting time in TiKV"},
	{name: "MAX_WAIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max waiting time in TiKV"},
	{name: "AVG_BACKOFF_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average waiting time before retry"},
	{name: "MAX_BACKOFF_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max waiting time before retry"},
	{name: "AVG_TOTAL_KEYS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average number of scanned keys"},
	{name: "MAX_TOTAL_KEYS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max number of scanned keys"},
	{name: "AVG_PROCESSED_KEYS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average number of processed keys"},
	{name: "MAX_PROCESSED_KEYS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max number of processed keys"},
	{name: "AVG_PREWRITE_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of prewrite phase"},
	{name: "MAX_PREWRITE_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max time of prewrite phase"},
	{name: "AVG_COMMIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of commit phase"},
	{name: "MAX_COMMIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max time of commit phase"},
	{name: "AVG_GET_COMMIT_TS_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of getting commit_ts"},
	{name: "MAX_GET_COMMIT_TS_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max time of getting commit_ts"},
	{name: "AVG_COMMIT_BACKOFF_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time before retry during commit phase"},
	{name: "MAX_COMMIT_BACKOFF_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max time before retry during commit phase"},
	{name: "AVG_RESOLVE_LOCK_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time for resolving locks"},
	{name: "MAX_RESOLVE_LOCK_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max time for resolving locks"},
	{name: "AVG_LOCAL_LATCH_WAIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average waiting time of local transaction"},
	{name: "MAX_LOCAL_LATCH_WAIT_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max waiting time of local transaction"},
	{name: "AVG_WRITE_KEYS", tp: mysql.TypeDouble, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average count of written keys"},
	{name: "MAX_WRITE_KEYS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max count of written keys"},
	{name: "AVG_WRITE_SIZE", tp: mysql.TypeDouble, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average amount of written bytes"},
	{name: "MAX_WRITE_SIZE", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max amount of written bytes"},
	{name: "AVG_PREWRITE_REGIONS", tp: mysql.TypeDouble, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average number of involved regions in prewrite phase"},
	{name: "MAX_PREWRITE_REGIONS", tp: mysql.TypeLong, size: 11, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max number of involved regions in prewrite phase"},
	{name: "AVG_TXN_RETRY", tp: mysql.TypeDouble, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average number of transaction retries"},
	{name: "MAX_TXN_RETRY", tp: mysql.TypeLong, size: 11, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max number of transaction retries"},
	{name: "SUM_EXEC_RETRY", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum number of execution retries in pessimistic transactions"},
	{name: "SUM_EXEC_RETRY_TIME", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum time of execution retries in pessimistic transactions"},
	{name: "SUM_BACKOFF_TIMES", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Sum of retries"},
	{name: "BACKOFF_TYPES", tp: mysql.TypeVarchar, size: 1024, comment: "Types of errors and the number of retries for each type"},
	{name: "AVG_MEM", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average memory(byte) used"},
	{name: "MAX_MEM", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max memory(byte) used"},
	{name: "AVG_DISK", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average disk space(byte) used"},
	{name: "MAX_DISK", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Max disk space(byte) used"},
	{name: "AVG_KV_TIME", tp: mysql.TypeLonglong, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of TiKV used"},
	{name: "AVG_PD_TIME", tp: mysql.TypeLonglong, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of PD used"},
	{name: "AVG_BACKOFF_TOTAL_TIME", tp: mysql.TypeLonglong, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of Backoff used"},
	{name: "AVG_WRITE_SQL_RESP_TIME", tp: mysql.TypeLonglong, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average time of write sql resp used"},
	{name: "PREPARED", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, comment: "Whether prepared"},
	{name: "AVG_AFFECTED_ROWS", tp: mysql.TypeDouble, size: 22, flag: mysql.NotNullFlag | mysql.UnsignedFlag, comment: "Average number of rows affected"},
	{name: "FIRST_SEEN", tp: mysql.TypeTimestamp, size: 26, flag: mysql.NotNullFlag, comment: "The time these statements are seen for the first time"},
	{name: "LAST_SEEN", tp: mysql.TypeTimestamp, size: 26, flag: mysql.NotNullFlag, comment: "The time these statements are seen for the last time"},
	{name: "PLAN_IN_CACHE", tp: mysql.TypeTiny, size: 1, flag: mysql.NotNullFlag, comment: "Whether the last statement hit plan cache"},
	{name: "PLAN_CACHE_HITS", tp: mysql.TypeLonglong, size: 20, flag: mysql.NotNullFlag, comment: "The number of times these statements hit plan cache"},
	{name: "QUERY_SAMPLE_TEXT", tp: mysql.TypeBlob, size: types.UnspecifiedLength, comment: "Sampled original statement"},
	{name: "PREV_SAMPLE_TEXT", tp: mysql.TypeBlob, size: types.UnspecifiedLength, comment: "The previous statement before commit"},
	{name: "PLAN_DIGEST", tp: mysql.TypeVarchar, size: 64, comment: "Digest of its execution plan"},
	{name: "PLAN", tp: mysql.TypeBlob, size: types.UnspecifiedLength, comment: "Sampled execution plan"},
}

var tableTableTiFlashTablesCols = []columnInfo{
	{name: "DATABASE", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "TIDB_DATABASE", tp: mysql.TypeVarchar, size: 64},
	{name: "TIDB_TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "IS_TOMBSTONE", tp: mysql.TypeLonglong, size: 64},
	{name: "SEGMENT_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_ROWS", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_DELETE_RANGES", tp: mysql.TypeLonglong, size: 64},
	{name: "DELTA_RATE_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_RATE_SEGMENTS", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_PLACED_RATE", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_CACHE_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "DELTA_CACHE_RATE", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_CACHE_WASTED_RATE", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_INDEX_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_SEGMENT_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_SEGMENT_SIZE", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_DELTA_ROWS", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_DELTA_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_DELTA_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_DELTA_SIZE", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_DELTA_DELETE_RANGES", tp: mysql.TypeDouble, size: 64},
	{name: "STABLE_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_STABLE_ROWS", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_STABLE_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "TOTAL_STABLE_SIZE_ON_DISK", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_STABLE_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_STABLE_SIZE", tp: mysql.TypeDouble, size: 64},
	{name: "TOTAL_PACK_COUNT_IN_DELTA", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_PACK_COUNT_IN_DELTA", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_PACK_ROWS_IN_DELTA", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_PACK_SIZE_IN_DELTA", tp: mysql.TypeDouble, size: 64},
	{name: "TOTAL_PACK_COUNT_IN_STABLE", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_PACK_COUNT_IN_STABLE", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_PACK_ROWS_IN_STABLE", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_PACK_SIZE_IN_STABLE", tp: mysql.TypeDouble, size: 64},
	{name: "STORAGE_STABLE_NUM_SNAPSHOTS", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_STABLE_NUM_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_STABLE_NUM_NORMAL_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_STABLE_MAX_PAGE_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_DELTA_NUM_SNAPSHOTS", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_DELTA_NUM_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_DELTA_NUM_NORMAL_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_DELTA_MAX_PAGE_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_META_NUM_SNAPSHOTS", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_META_NUM_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_META_NUM_NORMAL_PAGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STORAGE_META_MAX_PAGE_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "BACKGROUND_TASKS_LENGTH", tp: mysql.TypeLonglong, size: 64},
	{name: "TIFLASH_INSTANCE", tp: mysql.TypeVarchar, size: 64},
}

var tableTableTiFlashSegmentsCols = []columnInfo{
	{name: "DATABASE", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "TIDB_DATABASE", tp: mysql.TypeVarchar, size: 64},
	{name: "TIDB_TABLE", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "IS_TOMBSTONE", tp: mysql.TypeLonglong, size: 64},
	{name: "SEGMENT_ID", tp: mysql.TypeLonglong, size: 64},
	{name: "RANGE", tp: mysql.TypeVarchar, size: 64},
	{name: "ROWS", tp: mysql.TypeLonglong, size: 64},
	{name: "SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "DELETE_RANGES", tp: mysql.TypeLonglong, size: 64},
	{name: "STABLE_SIZE_ON_DISK", tp: mysql.TypeLonglong, size: 64},
	{name: "DELTA_PACK_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "STABLE_PACK_COUNT", tp: mysql.TypeLonglong, size: 64},
	{name: "AVG_DELTA_PACK_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "AVG_STABLE_PACK_ROWS", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_RATE", tp: mysql.TypeDouble, size: 64},
	{name: "DELTA_CACHE_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "DELTA_INDEX_SIZE", tp: mysql.TypeLonglong, size: 64},
	{name: "TIFLASH_INSTANCE", tp: mysql.TypeVarchar, size: 64},
}

var tableStorageStatsCols = []columnInfo{
	{name: "TABLE_SCHEMA", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_NAME", tp: mysql.TypeVarchar, size: 64},
	{name: "TABLE_ID", tp: mysql.TypeLonglong, size: 21},
	{name: "PEER_COUNT", tp: mysql.TypeLonglong, size: 21},
	{name: "REGION_COUNT", tp: mysql.TypeLonglong, size: 21, comment: "The region count of single replica of the table"},
	{name: "EMPTY_REGION_COUNT", tp: mysql.TypeLonglong, size: 21, comment: "The region count of single replica of the table"},
	{name: "TABLE_SIZE", tp: mysql.TypeLonglong, size: 64, comment: "The disk usage(MB) of single replica of the table, if the table size is empty or less than 1MB, it would show 1MB "},
	{name: "TABLE_KEYS", tp: mysql.TypeLonglong, size: 64, comment: "The count of keys of single replica of the table"},
}

// GetShardingInfo returns a nil or description string for the sharding information of given TableInfo.
// The returned description string may be:
//  - "NOT_SHARDED": for tables that SHARD_ROW_ID_BITS is not specified.
//  - "NOT_SHARDED(PK_IS_HANDLE)": for tables of which primary key is row id.
//  - "PK_AUTO_RANDOM_BITS={bit_number}": for tables of which primary key is sharded row id.
//  - "SHARD_BITS={bit_number}": for tables that with SHARD_ROW_ID_BITS.
// The returned nil indicates that sharding information is not suitable for the table(for example, when the table is a View).
// This function is exported for unit test.
func GetShardingInfo(dbInfo *model.DBInfo, tableInfo *model.TableInfo) interface{} {
	if dbInfo == nil || tableInfo == nil || tableInfo.IsView() || util.IsMemOrSysDB(dbInfo.Name.L) {
		return nil
	}
	shardingInfo := "NOT_SHARDED"
	if tableInfo.PKIsHandle {
		if tableInfo.ContainsAutoRandomBits() {
			shardingInfo = "PK_AUTO_RANDOM_BITS=" + strconv.Itoa(int(tableInfo.AutoRandomBits))
		} else {
			shardingInfo = "NOT_SHARDED(PK_IS_HANDLE)"
		}
	} else if tableInfo.ShardRowIDBits > 0 {
		shardingInfo = "SHARD_BITS=" + strconv.Itoa(int(tableInfo.ShardRowIDBits))
	}
	return shardingInfo
}

const (
	// PrimaryKeyType is the string constant of PRIMARY KEY.
	PrimaryKeyType = "PRIMARY KEY"
	// PrimaryConstraint is the string constant of PRIMARY.
	PrimaryConstraint = "PRIMARY"
	// UniqueKeyType is the string constant of UNIQUE.
	UniqueKeyType = "UNIQUE"
)

// ServerInfo represents the basic server information of single cluster component
type ServerInfo struct {
	ServerType     string
	Address        string
	StatusAddr     string
	Version        string
	GitHash        string
	StartTimestamp int64
}

func (s *ServerInfo) isLoopBackOrUnspecifiedAddr(addr string) bool {
	tcpAddr, err := net.ResolveTCPAddr("", addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(tcpAddr.IP.String())
	return ip != nil && (ip.IsUnspecified() || ip.IsLoopback())
}

// ResolveLoopBackAddr exports for testing.
func (s *ServerInfo) ResolveLoopBackAddr() {
	if s.isLoopBackOrUnspecifiedAddr(s.Address) && !s.isLoopBackOrUnspecifiedAddr(s.StatusAddr) {
		addr, err1 := net.ResolveTCPAddr("", s.Address)
		statusAddr, err2 := net.ResolveTCPAddr("", s.StatusAddr)
		if err1 == nil && err2 == nil {
			addr.IP = statusAddr.IP
			s.Address = addr.String()
		}
	} else if !s.isLoopBackOrUnspecifiedAddr(s.Address) && s.isLoopBackOrUnspecifiedAddr(s.StatusAddr) {
		addr, err1 := net.ResolveTCPAddr("", s.Address)
		statusAddr, err2 := net.ResolveTCPAddr("", s.StatusAddr)
		if err1 == nil && err2 == nil {
			statusAddr.IP = addr.IP
			s.StatusAddr = statusAddr.String()
		}
	}
}

// GetClusterServerInfo returns all components information of cluster
func GetClusterServerInfo(ctx sessionctx.Context) ([]ServerInfo, error) {
	failpoint.Inject("mockClusterInfo", func(val failpoint.Value) {
		// The cluster topology is injected by `failpoint` expression and
		// there is no extra checks for it. (let the test fail if the expression invalid)
		if s := val.(string); len(s) > 0 {
			var servers []ServerInfo
			for _, server := range strings.Split(s, ";") {
				parts := strings.Split(server, ",")
				servers = append(servers, ServerInfo{
					ServerType: parts[0],
					Address:    parts[1],
					StatusAddr: parts[2],
					Version:    parts[3],
					GitHash:    parts[4],
				})
			}
			failpoint.Return(servers, nil)
		}
	})

	type retriever func(ctx sessionctx.Context) ([]ServerInfo, error)
	var servers []ServerInfo
	for _, r := range []retriever{GetTiDBServerInfo, GetPDServerInfo, GetStoreServerInfo} {
		nodes, err := r(ctx)
		if err != nil {
			return nil, err
		}
		for i := range nodes {
			nodes[i].ResolveLoopBackAddr()
		}
		servers = append(servers, nodes...)
	}
	return servers, nil
}

// GetTiDBServerInfo returns all TiDB nodes information of cluster
func GetTiDBServerInfo(ctx sessionctx.Context) ([]ServerInfo, error) {
	// Get TiDB servers info.
	tidbNodes, err := infosync.GetAllServerInfo(context.Background())
	if err != nil {
		return nil, errors.Trace(err)
	}
	var servers []ServerInfo
	var isDefaultVersion bool
	if len(config.GetGlobalConfig().ServerVersion) == 0 {
		isDefaultVersion = true
	}
	for _, node := range tidbNodes {
		servers = append(servers, ServerInfo{
			ServerType:     "tidb",
			Address:        fmt.Sprintf("%s:%d", node.IP, node.Port),
			StatusAddr:     fmt.Sprintf("%s:%d", node.IP, node.StatusPort),
			Version:        FormatVersion(node.Version, isDefaultVersion),
			GitHash:        node.GitHash,
			StartTimestamp: node.StartTimestamp,
		})
	}
	return servers, nil
}

// FormatVersion make TiDBVersion consistent to TiKV and PD.
// The default TiDBVersion is 5.7.25-TiDB-${TiDBReleaseVersion}.
func FormatVersion(TiDBVersion string, isDefaultVersion bool) string {
	var version, nodeVersion string

	// The user hasn't set the config 'ServerVersion'.
	if isDefaultVersion {
		nodeVersion = TiDBVersion[strings.LastIndex(TiDBVersion, "TiDB-")+len("TiDB-"):]
		if nodeVersion[0] == 'v' {
			nodeVersion = nodeVersion[1:]
		}
		nodeVersions := strings.Split(nodeVersion, "-")
		if len(nodeVersions) == 1 {
			version = nodeVersions[0]
		} else if len(nodeVersions) >= 2 {
			version = fmt.Sprintf("%s-%s", nodeVersions[0], nodeVersions[1])
		}
	} else { // The user has already set the config 'ServerVersion',it would be a complex scene, so just use the 'ServerVersion' as version.
		version = TiDBVersion
	}

	return version
}

// GetPDServerInfo returns all PD nodes information of cluster
func GetPDServerInfo(ctx sessionctx.Context) ([]ServerInfo, error) {
	// Get PD servers info.
	store := ctx.GetStore()
	etcd, ok := store.(tikv.EtcdBackend)
	if !ok {
		return nil, errors.Errorf("%T not an etcd backend", store)
	}
	var servers []ServerInfo
	members, err := etcd.EtcdAddrs()
	if err != nil {
		return nil, errors.Trace(err)
	}
	for _, addr := range members {
		// Get PD version
		url := fmt.Sprintf("%s://%s%s", util.InternalHTTPSchema(), addr, pdapi.ClusterVersion)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, errors.Trace(err)
		}
		req.Header.Add("PD-Allow-follower-handle", "true")
		resp, err := util.InternalHTTPClient().Do(req)
		if err != nil {
			return nil, errors.Trace(err)
		}
		pdVersion, err := ioutil.ReadAll(resp.Body)
		terror.Log(resp.Body.Close())
		if err != nil {
			return nil, errors.Trace(err)
		}
		version := strings.Trim(strings.Trim(string(pdVersion), "\n"), "\"")

		// Get PD git_hash
		url = fmt.Sprintf("%s://%s%s", util.InternalHTTPSchema(), addr, pdapi.Status)
		req, err = http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, errors.Trace(err)
		}
		req.Header.Add("PD-Allow-follower-handle", "true")
		resp, err = util.InternalHTTPClient().Do(req)
		if err != nil {
			return nil, errors.Trace(err)
		}
		var content = struct {
			GitHash        string `json:"git_hash"`
			StartTimestamp int64  `json:"start_timestamp"`
		}{}
		err = json.NewDecoder(resp.Body).Decode(&content)
		terror.Log(resp.Body.Close())
		if err != nil {
			return nil, errors.Trace(err)
		}

		servers = append(servers, ServerInfo{
			ServerType:     "pd",
			Address:        addr,
			StatusAddr:     addr,
			Version:        version,
			GitHash:        content.GitHash,
			StartTimestamp: content.StartTimestamp,
		})
	}
	return servers, nil
}

const tiflashLabel = "tiflash"

// GetStoreServerInfo returns all store nodes(TiKV or TiFlash) cluster information
func GetStoreServerInfo(ctx sessionctx.Context) ([]ServerInfo, error) {
	isTiFlashStore := func(store *metapb.Store) bool {
		isTiFlash := false
		for _, label := range store.Labels {
			if label.GetKey() == "engine" && label.GetValue() == tiflashLabel {
				isTiFlash = true
			}
		}
		return isTiFlash
	}

	store := ctx.GetStore()
	// Get TiKV servers info.
	tikvStore, ok := store.(tikv.Storage)
	if !ok {
		return nil, errors.Errorf("%T is not an TiKV or TiFlash store instance", store)
	}
	pdClient := tikvStore.GetRegionCache().PDClient()
	if pdClient == nil {
		return nil, errors.New("pd unavailable")
	}
	stores, err := pdClient.GetAllStores(context.Background())
	if err != nil {
		return nil, errors.Trace(err)
	}
	var servers []ServerInfo
	for _, store := range stores {
		failpoint.Inject("mockStoreTombstone", func(val failpoint.Value) {
			if val.(bool) {
				store.State = metapb.StoreState_Tombstone
			}
		})

		if store.GetState() == metapb.StoreState_Tombstone {
			continue
		}
		var tp string
		if isTiFlashStore(store) {
			tp = tiflashLabel
		} else {
			tp = tikv.GetStoreTypeByMeta(store).Name()
		}
		servers = append(servers, ServerInfo{
			ServerType:     tp,
			Address:        store.Address,
			StatusAddr:     store.StatusAddress,
			Version:        store.Version,
			GitHash:        store.GitHash,
			StartTimestamp: store.StartTimestamp,
		})
	}
	return servers, nil
}

// GetTiFlashStoreCount returns the count of tiflash server.
func GetTiFlashStoreCount(ctx sessionctx.Context) (cnt uint64, err error) {
	failpoint.Inject("mockTiFlashStoreCount", func(val failpoint.Value) {
		if val.(bool) {
			failpoint.Return(uint64(10), nil)
		}
	})

	stores, err := GetStoreServerInfo(ctx)
	if err != nil {
		return cnt, err
	}
	for _, store := range stores {
		if store.ServerType == tiflashLabel {
			cnt++
		}
	}
	return cnt, nil
}

var tableNameToColumns = map[string][]columnInfo{
	TableSchemata:                             schemataCols,
	TableTables:                               tablesCols,
	TableColumns:                              columnsCols,
	tableColumnStatistics:                     columnStatisticsCols,
	TableStatistics:                           statisticsCols,
	TableCharacterSets:                        charsetCols,
	TableCollations:                           collationsCols,
	tableFiles:                                filesCols,
	TableProfiling:                            profilingCols,
	TablePartitions:                           partitionsCols,
	TableKeyColumn:                            keyColumnUsageCols,
	tableReferConst:                           referConstCols,
	TableSessionVar:                           sessionVarCols,
	tablePlugins:                              pluginsCols,
	TableConstraints:                          tableConstraintsCols,
	tableTriggers:                             tableTriggersCols,
	TableUserPrivileges:                       tableUserPrivilegesCols,
	tableSchemaPrivileges:                     tableSchemaPrivilegesCols,
	tableTablePrivileges:                      tableTablePrivilegesCols,
	tableColumnPrivileges:                     tableColumnPrivilegesCols,
	TableEngines:                              tableEnginesCols,
	TableViews:                                tableViewsCols,
	tableRoutines:                             tableRoutinesCols,
	tableParameters:                           tableParametersCols,
	tableEvents:                               tableEventsCols,
	tableGlobalStatus:                         tableGlobalStatusCols,
	tableGlobalVariables:                      tableGlobalVariablesCols,
	tableSessionStatus:                        tableSessionStatusCols,
	tableOptimizerTrace:                       tableOptimizerTraceCols,
	tableTableSpaces:                          tableTableSpacesCols,
	TableCollationCharacterSetApplicability:   tableCollationCharacterSetApplicabilityCols,
	TableProcesslist:                          tableProcesslistCols,
	TableTiDBIndexes:                          tableTiDBIndexesCols,
	TableSlowQuery:                            slowQueryCols,
	TableTiDBHotRegions:                       TableTiDBHotRegionsCols,
	tableTiKVStoreStatus:                      tableTiKVStoreStatusCols,
	TableAnalyzeStatus:                        tableAnalyzeStatusCols,
	tableTiKVRegionStatus:                     tableTiKVRegionStatusCols,
	TableTiKVRegionPeers:                      TableTiKVRegionPeersCols,
	TableTiDBServersInfo:                      tableTiDBServersInfoCols,
	TableClusterInfo:                          tableClusterInfoCols,
	TableClusterConfig:                        tableClusterConfigCols,
	TableClusterLog:                           tableClusterLogCols,
	TableClusterLoad:                          tableClusterLoadCols,
	TableTiFlashReplica:                       tableTableTiFlashReplicaCols,
	TableClusterHardware:                      tableClusterHardwareCols,
	TableClusterSystemInfo:                    tableClusterSystemInfoCols,
	TableInspectionResult:                     tableInspectionResultCols,
	TableMetricSummary:                        tableMetricSummaryCols,
	TableMetricSummaryByLabel:                 tableMetricSummaryByLabelCols,
	TableMetricTables:                         tableMetricTablesCols,
	TableInspectionSummary:                    tableInspectionSummaryCols,
	TableInspectionRules:                      tableInspectionRulesCols,
	TableDDLJobs:                              tableDDLJobsCols,
	TableSequences:                            tableSequencesCols,
	TableStatementsSummary:                    tableStatementsSummaryCols,
	TableStatementsSummaryHistory:             tableStatementsSummaryCols,
	TableTiFlashTables:                        tableTableTiFlashTablesCols,
	TableTiFlashSegments:                      tableTableTiFlashSegmentsCols,
	TableStorageStats:                         tableStorageStatsCols,
	TablePgInformationsSchemaCatalogName:      pgTableInformationSchemaCatalogNameCols,
	TablePgAdministrableRoleAuthorizations:    pgTableAdministrableRoleAuthorizationsCols,
	TablePgApplicableRole:                     pgTableApplicableRolesCols,
	TablePgAttributes:                         pgTableAttributesCols,
	TablePgCharacterSets:                      pgTableCharacterSetsCols,
	TablePgCheckConstraintRoutineUsage:        pgTableCheckConstraintRoutineUsageCols,
	TablePgCheckConstraints:                   pgTableCheckConstraintsCols,
	TablePgCollations:                         pgTableCollationsCols,
	TablePgCollationCharacterSetApplicability: pgTableCollationCharacterSetApplicabilityCols,
	TablePgColumnColumnUsage:                  pgTableColumnColumnUsageCols,
	TablePgColumnDomainUsage:                  pgTableColumnDomainUsageCols,
	TablePgColumnOptions:                      pgTableColumnOptionsCols,
	TablePgColumnPrivileges:                   pgTableColumnPrivilegesCols,
	TablePgColumnUdtUsage:                     pgTableColumnUdtUsageCols,
	TablePgColumns:                            pgTableColumnsCols,
	TablePgConstraintColumnUsage:              pgTableConstraintColumnUsageCols,
	TablePgConstraintTableUsage:               pgTableConstraintTableUsageCols,
	TablePgDataTypePrivileges:                 pgTableDataTypePrivilegesCols,
	TablePgDomainConstraints:                  pgTableDomainConstraintsCols,
	TablePgDomainUdtUsage:                     pgTableDomainUdtUsageCols,
	TablePgDomains:                            pgTableDomainsCols,
	TablePgElementTypes:                       pgTableElementTypesCols,
	TablePgEnabledRoles:                       pgTableEnabledRolesCols,
	TablePgForeignDataWrapperOptions:          pgTableForeignDataWrapperOptionsCols,
	TablePgForeignDataWrappers:                pgTableForeignDataWrappersCols,
	TablePgForeignServerOptions:               pgTableForeignServerOptionsCols,
	TablePgForeignServers:                     pgTableForeignServers,
	TablePgForeignTableOptions:                pgTableForeignTableOptionsCols,
	TablePgForeignTales:                       pgTableForeignTablesCols,
	TablePgKeyColumnUsage:                     pgTableKeyColumnUsageCols,
	TablePgParameters:                         pgTableParametersCols,
	TablePgReferentialConstraints:             pgTableReferentialConstraintsCols,
	TablePgRoleColumnGrants:                   pgTableRoleColumnGrantsCols,
	TablePgRoleRoutineGrants:                  pgTableRoleRoutineGrantsCols,
	TablePgRoleTableGrants:                    pgTableRoleTableGrantsCols,
	TablePgRoleUdtGrants:                      pgTableRoleUdtGrantsCols,
	TablePgRoleUsageGrants:                    pgTableRoleUsageGrantsCols,
	TablePgRoutinePrivileges:                  pgTableRoutinePrivilegesCols,
	TablePgRoutines:                           pgTableRoutinesCols,
	TablePgSchemata:                           pgTableSchemataCols,
	TablePgSequences:                          pgTableSequencesCols,
	TablePgSQLFeatures:                        pgTableSQLFeaturesCols,
	TablePgSQLImplementationInfo:              pgTableSQLImplementationInfoCols,
	TablePgSQLParts:                           pgTableSQLPartsCols,
	TablePgSQLSizing:                          pgTableSQLSizingCols,
	TablePgTableConstraints:                   pgTableTableConstraintsCols,
	TablePgTablePrivileges:                    pgTableTablePrivilegesCols,
	TablePgTables:                             pgTableTablesCols,
	TablePgTransforms:                         pgTableTransformsCols,
	TablePgTriggeredUpdateColumns:             pgTableTriggeredUpdateColumns,
	TablePgTriggers:                           pgTableTriggersCols,
	TablePgUdtPrivileges:                      pgTableUdtPrivilegesCols,
	TablePgUsagePrivileges:                    pgTableUsagePrivilegesCols,
	TablePgUserDefinedTypes:                   pgTableUserDefinedTypesCols,
	TablePgUserMappingOptions:                 pgTableUserMappingOptionsCols,
	TablePgUserMappings:                       pgTableUserMappingsCols,
	TablePgViewColumnUsage:                    pgTableViewColumnUsageCols,
	TablePgViewRoutineUsage:                   pgTableViewRoutineUsageCols,
	TablePgViewTableUsage:                     pgTableViewTableUsageCols,
	TablePgViews:                              pgTableViewsCols,
}

func createInfoSchemaTable(_ autoid.Allocators, meta *model.TableInfo) (table.Table, error) {
	columns := make([]*table.Column, len(meta.Columns))
	for i, col := range meta.Columns {
		columns[i] = table.ToColumn(col)
	}
	tp := table.VirtualTable
	if isClusterTableByName(util.InformationSchemaName.O, meta.Name.O) {
		tp = table.ClusterTable
	}
	return &infoschemaTable{meta: meta, cols: columns, tp: tp}, nil
}

type infoschemaTable struct {
	meta *model.TableInfo
	cols []*table.Column
	tp   table.Type
}

// SchemasSorter implements the sort.Interface interface, sorts DBInfo by name.
type SchemasSorter []*model.DBInfo

func (s SchemasSorter) Len() int {
	return len(s)
}

func (s SchemasSorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s SchemasSorter) Less(i, j int) bool {
	return s[i].Name.L < s[j].Name.L
}

func (it *infoschemaTable) getRows(ctx sessionctx.Context, cols []*table.Column) (fullRows [][]types.Datum, err error) {
	is := GetInfoSchema(ctx)
	dbs := is.AllSchemas()
	sort.Sort(SchemasSorter(dbs))
	switch it.meta.Name.O {
	case tableFiles:
	case tableReferConst:
	case tablePlugins, tableTriggers:
	case tableRoutines:
	// TODO: Fill the following tables.
	case tableSchemaPrivileges:
	case tableTablePrivileges:
	case tableColumnPrivileges:
	case tableParameters:
	case tableEvents:
	case tableGlobalStatus:
	case tableGlobalVariables:
	case tableSessionStatus:
	case tableOptimizerTrace:
	case tableTableSpaces:
	case tableTiKVStoreStatus:
		fullRows, err = dataForTiKVStoreStatus(ctx)
	case tableTiKVRegionStatus:
		fullRows, err = dataForTiKVRegionStatus(ctx)
	}
	if err != nil {
		return nil, err
	}
	if len(cols) == len(it.cols) {
		return
	}
	rows := make([][]types.Datum, len(fullRows))
	for i, fullRow := range fullRows {
		row := make([]types.Datum, len(cols))
		for j, col := range cols {
			row[j] = fullRow[col.Offset]
		}
		rows[i] = row
	}
	return rows, nil
}

// IterRecords implements table.Table IterRecords interface.
func (it *infoschemaTable) IterRecords(ctx sessionctx.Context, startKey kv.Key, cols []*table.Column,
	fn table.RecordIterFunc) error {
	if len(startKey) != 0 {
		return table.ErrUnsupportedOp
	}
	rows, err := it.getRows(ctx, cols)
	if err != nil {
		return err
	}
	for i, row := range rows {
		more, err := fn(int64(i), row, cols)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	return nil
}

// RowWithCols implements table.Table RowWithCols interface.
func (it *infoschemaTable) RowWithCols(ctx sessionctx.Context, h int64, cols []*table.Column) ([]types.Datum, error) {
	return nil, table.ErrUnsupportedOp
}

// Row implements table.Table Row interface.
func (it *infoschemaTable) Row(ctx sessionctx.Context, h int64) ([]types.Datum, error) {
	return nil, table.ErrUnsupportedOp
}

// Cols implements table.Table Cols interface.
func (it *infoschemaTable) Cols() []*table.Column {
	return it.cols
}

// VisibleCols implements table.Table VisibleCols interface.
func (it *infoschemaTable) VisibleCols() []*table.Column {
	return it.cols
}

// HiddenCols implements table.Table HiddenCols interface.
func (it *infoschemaTable) HiddenCols() []*table.Column {
	return nil
}

// WritableCols implements table.Table WritableCols interface.
func (it *infoschemaTable) WritableCols() []*table.Column {
	return it.cols
}

// DeletableCols implements table DeletableCols interface.
func (it *infoschemaTable) DeletableCols() []*table.Column {
	return it.cols
}

// Indices implements table.Table Indices interface.
func (it *infoschemaTable) Indices() []table.Index {
	return nil
}

// WritableIndices implements table.Table WritableIndices interface.
func (it *infoschemaTable) WritableIndices() []table.Index {
	return nil
}

// DeletableIndices implements table.Table DeletableIndices interface.
func (it *infoschemaTable) DeletableIndices() []table.Index {
	return nil
}

// RecordPrefix implements table.Table RecordPrefix interface.
func (it *infoschemaTable) RecordPrefix() kv.Key {
	return nil
}

// IndexPrefix implements table.Table IndexPrefix interface.
func (it *infoschemaTable) IndexPrefix() kv.Key {
	return nil
}

// FirstKey implements table.Table FirstKey interface.
func (it *infoschemaTable) FirstKey() kv.Key {
	return nil
}

// RecordKey implements table.Table RecordKey interface.
func (it *infoschemaTable) RecordKey(h int64) kv.Key {
	return nil
}

// AddRecord implements table.Table AddRecord interface.
func (it *infoschemaTable) AddRecord(ctx sessionctx.Context, r []types.Datum, opts ...table.AddRecordOption) (recordID int64, err error) {
	return 0, table.ErrUnsupportedOp
}

// RemoveRecord implements table.Table RemoveRecord interface.
func (it *infoschemaTable) RemoveRecord(ctx sessionctx.Context, h int64, r []types.Datum) error {
	return table.ErrUnsupportedOp
}

// UpdateRecord implements table.Table UpdateRecord interface.
func (it *infoschemaTable) UpdateRecord(ctx context.Context, sctx sessionctx.Context, h int64, oldData, newData []types.Datum, touched []bool) error {
	return table.ErrUnsupportedOp
}

// Allocators implements table.Table Allocators interface.
func (it *infoschemaTable) Allocators(_ sessionctx.Context) autoid.Allocators {
	return nil
}

// RebaseAutoID implements table.Table RebaseAutoID interface.
func (it *infoschemaTable) RebaseAutoID(ctx sessionctx.Context, newBase int64, isSetStep bool, tp autoid.AllocatorType) error {
	return table.ErrUnsupportedOp
}

// Meta implements table.Table Meta interface.
func (it *infoschemaTable) Meta() *model.TableInfo {
	return it.meta
}

// GetPhysicalID implements table.Table GetPhysicalID interface.
func (it *infoschemaTable) GetPhysicalID() int64 {
	return it.meta.ID
}

// Seek implements table.Table Seek interface.
func (it *infoschemaTable) Seek(ctx sessionctx.Context, h int64) (int64, bool, error) {
	return 0, false, table.ErrUnsupportedOp
}

// Type implements table.Table Type interface.
func (it *infoschemaTable) Type() table.Type {
	return it.tp
}

// VirtualTable is a dummy table.Table implementation.
type VirtualTable struct{}

// IterRecords implements table.Table IterRecords interface.
func (vt *VirtualTable) IterRecords(ctx sessionctx.Context, startKey kv.Key, cols []*table.Column,
	fn table.RecordIterFunc) error {
	if len(startKey) != 0 {
		return table.ErrUnsupportedOp
	}
	return nil
}

// RowWithCols implements table.Table RowWithCols interface.
func (vt *VirtualTable) RowWithCols(ctx sessionctx.Context, h int64, cols []*table.Column) ([]types.Datum, error) {
	return nil, table.ErrUnsupportedOp
}

// Row implements table.Table Row interface.
func (vt *VirtualTable) Row(ctx sessionctx.Context, h int64) ([]types.Datum, error) {
	return nil, table.ErrUnsupportedOp
}

// Cols implements table.Table Cols interface.
func (vt *VirtualTable) Cols() []*table.Column {
	return nil
}

// VisibleCols implements table.Table VisibleCols interface.
func (vt *VirtualTable) VisibleCols() []*table.Column {
	return nil
}

// HiddenCols implements table.Table HiddenCols interface.
func (vt *VirtualTable) HiddenCols() []*table.Column {
	return nil
}

// WritableCols implements table.Table WritableCols interface.
func (vt *VirtualTable) WritableCols() []*table.Column {
	return nil
}

// DeletableCols implements table DeletableCols interface.
func (vt *VirtualTable) DeletableCols() []*table.Column {
	return nil
}

// Indices implements table.Table Indices interface.
func (vt *VirtualTable) Indices() []table.Index {
	return nil
}

// WritableIndices implements table.Table WritableIndices interface.
func (vt *VirtualTable) WritableIndices() []table.Index {
	return nil
}

// DeletableIndices implements table.Table DeletableIndices interface.
func (vt *VirtualTable) DeletableIndices() []table.Index {
	return nil
}

// RecordPrefix implements table.Table RecordPrefix interface.
func (vt *VirtualTable) RecordPrefix() kv.Key {
	return nil
}

// IndexPrefix implements table.Table IndexPrefix interface.
func (vt *VirtualTable) IndexPrefix() kv.Key {
	return nil
}

// FirstKey implements table.Table FirstKey interface.
func (vt *VirtualTable) FirstKey() kv.Key {
	return nil
}

// RecordKey implements table.Table RecordKey interface.
func (vt *VirtualTable) RecordKey(h int64) kv.Key {
	return nil
}

// AddRecord implements table.Table AddRecord interface.
func (vt *VirtualTable) AddRecord(ctx sessionctx.Context, r []types.Datum, opts ...table.AddRecordOption) (recordID int64, err error) {
	return 0, table.ErrUnsupportedOp
}

// RemoveRecord implements table.Table RemoveRecord interface.
func (vt *VirtualTable) RemoveRecord(ctx sessionctx.Context, h int64, r []types.Datum) error {
	return table.ErrUnsupportedOp
}

// UpdateRecord implements table.Table UpdateRecord interface.
func (vt *VirtualTable) UpdateRecord(ctx context.Context, sctx sessionctx.Context, h int64, oldData, newData []types.Datum, touched []bool) error {
	return table.ErrUnsupportedOp
}

// Allocators implements table.Table Allocators interface.
func (vt *VirtualTable) Allocators(_ sessionctx.Context) autoid.Allocators {
	return nil
}

// RebaseAutoID implements table.Table RebaseAutoID interface.
func (vt *VirtualTable) RebaseAutoID(ctx sessionctx.Context, newBase int64, isSetStep bool, tp autoid.AllocatorType) error {
	return table.ErrUnsupportedOp
}

// Meta implements table.Table Meta interface.
func (vt *VirtualTable) Meta() *model.TableInfo {
	return nil
}

// GetPhysicalID implements table.Table GetPhysicalID interface.
func (vt *VirtualTable) GetPhysicalID() int64 {
	return 0
}

// Seek implements table.Table Seek interface.
func (vt *VirtualTable) Seek(ctx sessionctx.Context, h int64) (int64, bool, error) {
	return 0, false, table.ErrUnsupportedOp
}

// Type implements table.Table Type interface.
func (vt *VirtualTable) Type() table.Type {
	return table.VirtualTable
}
