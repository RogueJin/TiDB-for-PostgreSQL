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

package util

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DigitalChinaOpenSource/DCParser"
	"github.com/DigitalChinaOpenSource/DCParser/model"
	"github.com/DigitalChinaOpenSource/DCParser/mysql"
	"github.com/DigitalChinaOpenSource/DCParser/terror"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/metrics"
	"github.com/pingcap/tidb/util/collate"
	"github.com/pingcap/tidb/util/logutil"
	"github.com/pingcap/tipb/go-tipb"
	"go.uber.org/zap"
)

const (
	// DefaultMaxRetries indicates the max retry count.
	DefaultMaxRetries = 30
	// RetryInterval indicates retry interval.
	RetryInterval uint64 = 500
	// GCTimeFormat is the format that gc_worker used to store times.
	GCTimeFormat = "20060102-15:04:05 -0700"
)

// RunWithRetry will run the f with backoff and retry.
// retryCnt: Max retry count
// backoff: When run f failed, it will sleep backoff * triedCount time.Millisecond.
// Function f should have two return value. The first one is an bool which indicate if the err if retryable.
// The second is if the f meet any error.
func RunWithRetry(retryCnt int, backoff uint64, f func() (bool, error)) (err error) {
	for i := 1; i <= retryCnt; i++ {
		var retryAble bool
		retryAble, err = f()
		if err == nil || !retryAble {
			return errors.Trace(err)
		}
		sleepTime := time.Duration(backoff*uint64(i)) * time.Millisecond
		time.Sleep(sleepTime)
	}
	return errors.Trace(err)
}

// GetStack gets the stacktrace.
func GetStack() []byte {
	const size = 4096
	buf := make([]byte, size)
	stackSize := runtime.Stack(buf, false)
	buf = buf[:stackSize]
	return buf
}

// WithRecovery wraps goroutine startup call with force recovery.
// it will dump current goroutine stack into log if catch any recover result.
//   exec:      execute logic function.
//   recoverFn: handler will be called after recover and before dump stack, passing `nil` means noop.
func WithRecovery(exec func(), recoverFn func(r interface{})) {
	defer func() {
		r := recover()
		if recoverFn != nil {
			recoverFn(r)
		}
		if r != nil {
			logutil.BgLogger().Error("panic in the recoverable goroutine",
				zap.Reflect("r", r),
				zap.Stack("stack trace"))
		}
	}()
	exec()
}

// Recover includes operations such as recovering, clearing，and printing information.
// It will dump current goroutine stack into log if catch any recover result.
//   metricsLabel: The label of PanicCounter metrics.
//   funcInfo:     Some information for the panic function.
//   recoverFn:    Handler will be called after recover and before dump stack, passing `nil` means noop.
//   quit:         If this value is true, the current program exits after recovery.
func Recover(metricsLabel, funcInfo string, recoverFn func(), quit bool) {
	r := recover()
	if r == nil {
		return
	}

	if recoverFn != nil {
		recoverFn()
	}
	logutil.BgLogger().Error("panic in the recoverable goroutine",
		zap.String("label", metricsLabel),
		zap.String("funcInfo", funcInfo),
		zap.Reflect("r", r),
		zap.String("stack", string(GetStack())))
	metrics.PanicCounter.WithLabelValues(metricsLabel).Inc()
	if quit {
		// Wait for metrics to be pushed.
		time.Sleep(time.Second * 15)
		os.Exit(1)
	}
}

// CompatibleParseGCTime parses a string with `GCTimeFormat` and returns a time.Time. If `value` can't be parsed as that
// format, truncate to last space and try again. This function is only useful when loading times that saved by
// gc_worker. We have changed the format that gc_worker saves time (removed the last field), but when loading times it
// should be compatible with the old format.
func CompatibleParseGCTime(value string) (time.Time, error) {
	t, err := time.Parse(GCTimeFormat, value)

	if err != nil {
		// Remove the last field that separated by space
		parts := strings.Split(value, " ")
		prefix := strings.Join(parts[:len(parts)-1], " ")
		t, err = time.Parse(GCTimeFormat, prefix)
	}

	if err != nil {
		err = errors.Errorf("string \"%v\" doesn't has a prefix that matches format \"%v\"", value, GCTimeFormat)
	}
	return t, err
}

// HasCancelled checks whether context has be cancelled.
func HasCancelled(ctx context.Context) (cancel bool) {
	select {
	case <-ctx.Done():
		cancel = true
	default:
	}
	return
}

const (
	// syntaxErrorPrefix is the common prefix for SQL syntax error in TiDB.
	syntaxErrorPrefix = "You have an error in your SQL syntax; check the manual that corresponds to your TiDB version for the right syntax to use"
)

// SyntaxError converts parser error to TiDB's syntax error.
func SyntaxError(err error) error {
	if err == nil {
		return nil
	}
	logutil.BgLogger().Debug("syntax error", zap.Error(err))

	// If the error is already a terror with stack, pass it through.
	if errors.HasStack(err) {
		cause := errors.Cause(err)
		if _, ok := cause.(*terror.Error); ok {
			return err
		}
	}

	return parser.ErrParse.GenWithStackByArgs(syntaxErrorPrefix, err.Error())
}

// SyntaxWarn converts parser warn to TiDB's syntax warn.
func SyntaxWarn(err error) error {
	if err == nil {
		return nil
	}
	return parser.ErrParse.GenWithStackByArgs(syntaxErrorPrefix, err.Error())
}

var (
	// InformationSchemaName is the `INFORMATION_SCHEMA` database name.
	InformationSchemaName = model.NewCIStr("INFORMATION_SCHEMA")
	// PerformanceSchemaName is the `PERFORMANCE_SCHEMA` database name.
	PerformanceSchemaName = model.NewCIStr("PERFORMANCE_SCHEMA")
	// MetricSchemaName is the `METRICS_SCHEMA` database name.
	MetricSchemaName = model.NewCIStr("METRICS_SCHEMA")
	// PgCatalogName is the `pg_catalog` database name.
	PgCatalogName = model.NewCIStr("pg_catalog")
)

// IsMemOrSysDB uses to check whether dbLowerName is memory database or system database.
func IsMemOrSysDB(dbLowerName string) bool {
	switch dbLowerName {
	case InformationSchemaName.L,
		PerformanceSchemaName.L,
		mysql.SystemDB,
		MetricSchemaName.L:
		return true
	}
	return false
}

// X509NameOnline prints pkix.Name into old X509_NAME_oneline format.
// https://www.openssl.org/docs/manmaster/man3/X509_NAME_oneline.html
func X509NameOnline(n pkix.Name) string {
	s := make([]string, 0, len(n.Names))
	for _, name := range n.Names {
		oid := name.Type.String()
		// unlike MySQL, TiDB only support check pkixAttributeTypeNames fields
		if n, exist := pkixAttributeTypeNames[oid]; exist {
			s = append(s, n+"="+fmt.Sprint(name.Value))
		}
	}
	if len(s) == 0 {
		return ""
	}
	return "/" + strings.Join(s, "/")
}

const (
	// Country is type name for country.
	Country = "C"
	// Organization is type name for organization.
	Organization = "O"
	// OrganizationalUnit is type name for organizational unit.
	OrganizationalUnit = "OU"
	// Locality is type name for locality.
	Locality = "L"
	// Email is type name for email.
	Email = "emailAddress"
	// CommonName is type name for common name.
	CommonName = "CN"
	// Province is type name for province or state.
	Province = "ST"
)

// see go/src/crypto/x509/pkix/pkix.go:attributeTypeNames
var pkixAttributeTypeNames = map[string]string{
	"2.5.4.6":              Country,
	"2.5.4.10":             Organization,
	"2.5.4.11":             OrganizationalUnit,
	"2.5.4.3":              CommonName,
	"2.5.4.5":              "SERIALNUMBER",
	"2.5.4.7":              Locality,
	"2.5.4.8":              Province,
	"2.5.4.9":              "STREET",
	"2.5.4.17":             "POSTALCODE",
	"1.2.840.113549.1.9.1": Email,
}

var pkixTypeNameAttributes = make(map[string]string)

// MockPkixAttribute generates mock AttributeTypeAndValue.
// only used for test.
func MockPkixAttribute(name, value string) pkix.AttributeTypeAndValue {
	n, exists := pkixTypeNameAttributes[name]
	if !exists {
		panic(fmt.Sprintf("unsupport mock type: %s", name))
	}
	var vs []int
	for _, v := range strings.Split(n, ".") {
		i, err := strconv.Atoi(v)
		if err != nil {
			panic(err)
		}
		vs = append(vs, i)
	}
	return pkix.AttributeTypeAndValue{
		Type:  vs,
		Value: value,
	}
}

// SANType is enum value for GlobalPrivValue.SANs keys.
type SANType string

const (
	// URI indicates uri info in SAN.
	URI = SANType("URI")
	// DNS indicates dns info in SAN.
	DNS = SANType("DNS")
	// IP indicates ip info in SAN.
	IP = SANType("IP")
)

var supportSAN = map[SANType]struct{}{
	URI: {},
	DNS: {},
	IP:  {},
}

// ParseAndCheckSAN parses and check SAN str.
func ParseAndCheckSAN(san string) (map[SANType][]string, error) {
	sanMap := make(map[SANType][]string)
	sans := strings.Split(san, ",")
	for _, san := range sans {
		kv := strings.SplitN(san, ":", 2)
		if len(kv) != 2 {
			return nil, errors.Errorf("invalid SAN value %s", san)
		}
		k, v := SANType(strings.ToUpper(strings.TrimSpace(kv[0]))), strings.TrimSpace(kv[1])
		if _, s := supportSAN[k]; !s {
			return nil, errors.Errorf("unsupported SAN key %s, current only support %v", k, supportSAN)
		}
		sanMap[k] = append(sanMap[k], v)
	}
	return sanMap, nil
}

// CheckSupportX509NameOneline parses and validate input str is X509_NAME_oneline format
// and precheck check-item is supported by TiDB
// https://www.openssl.org/docs/manmaster/man3/X509_NAME_oneline.html
func CheckSupportX509NameOneline(oneline string) (err error) {
	entries := strings.Split(oneline, `/`)
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		kvs := strings.Split(entry, "=")
		if len(kvs) != 2 {
			err = errors.Errorf("invalid X509_NAME input: %s", oneline)
			return
		}
		k := kvs[0]
		if _, support := pkixTypeNameAttributes[k]; !support {
			err = errors.Errorf("Unsupport check '%s' in current version TiDB", k)
			return
		}
	}
	return
}

var tlsCipherString = map[uint16]string{
	tls.TLS_RSA_WITH_RC4_128_SHA:                "RC4-SHA",
	tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA:           "DES-CBC3-SHA",
	tls.TLS_RSA_WITH_AES_128_CBC_SHA:            "AES128-SHA",
	tls.TLS_RSA_WITH_AES_256_CBC_SHA:            "AES256-SHA",
	tls.TLS_RSA_WITH_AES_128_CBC_SHA256:         "AES128-SHA256",
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256:         "AES128-GCM-SHA256",
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384:         "AES256-GCM-SHA384",
	tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA:        "ECDHE-ECDSA-RC4-SHA",
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA:    "ECDHE-ECDSA-AES128-SHA",
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA:    "ECDHE-ECDSA-AES256-SHA",
	tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA:          "ECDHE-RSA-RC4-SHA",
	tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA:     "ECDHE-RSA-DES-CBC3-SHA",
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:      "ECDHE-RSA-AES128-SHA",
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:      "ECDHE-RSA-AES256-SHA",
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256: "ECDHE-ECDSA-AES128-SHA256",
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256:   "ECDHE-RSA-AES128-SHA256",
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:   "ECDHE-RSA-AES128-GCM-SHA256",
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256: "ECDHE-ECDSA-AES128-GCM-SHA256",
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:   "ECDHE-RSA-AES256-GCM-SHA384",
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384: "ECDHE-ECDSA-AES256-GCM-SHA384",
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305:    "ECDHE-RSA-CHACHA20-POLY1305",
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:  "ECDHE-ECDSA-CHACHA20-POLY1305",
	// TLS 1.3 cipher suites, compatible with mysql using '_'.
	tls.TLS_AES_128_GCM_SHA256:       "TLS_AES_128_GCM_SHA256",
	tls.TLS_AES_256_GCM_SHA384:       "TLS_AES_256_GCM_SHA384",
	tls.TLS_CHACHA20_POLY1305_SHA256: "TLS_CHACHA20_POLY1305_SHA256",
}

// SupportCipher maintains cipher supported by TiDB.
var SupportCipher = make(map[string]struct{}, len(tlsCipherString))

// TLSCipher2String convert tls num to string.
// Taken from https://testssl.sh/openssl-rfc.mapping.html .
func TLSCipher2String(n uint16) string {
	s, ok := tlsCipherString[n]
	if !ok {
		return ""
	}
	return s
}

// ColumnsToProto converts a slice of model.ColumnInfo to a slice of tipb.ColumnInfo.
func ColumnsToProto(columns []*model.ColumnInfo, pkIsHandle bool) []*tipb.ColumnInfo {
	cols := make([]*tipb.ColumnInfo, 0, len(columns))
	for _, c := range columns {
		col := ColumnToProto(c)
		// TODO: Here `PkHandle`'s meaning is changed, we will change it to `IsHandle` when tikv's old select logic
		// is abandoned.
		if (pkIsHandle && mysql.HasPriKeyFlag(c.Flag)) || c.ID == model.ExtraHandleID {
			col.PkHandle = true
		} else {
			col.PkHandle = false
		}
		cols = append(cols, col)
	}
	return cols
}

// ColumnToProto converts model.ColumnInfo to tipb.ColumnInfo.
func ColumnToProto(c *model.ColumnInfo) *tipb.ColumnInfo {
	pc := &tipb.ColumnInfo{
		ColumnId:  c.ID,
		Collation: collate.RewriteNewCollationIDIfNeeded(int32(mysql.CollationNames[c.FieldType.Collate])),
		ColumnLen: int32(c.FieldType.Flen),
		Decimal:   int32(c.FieldType.Decimal),
		Flag:      int32(c.Flag),
		Elems:     c.Elems,
	}
	pc.Tp = int32(c.FieldType.Tp)
	return pc
}

func init() {
	for _, value := range tlsCipherString {
		SupportCipher[value] = struct{}{}
	}
	for key, value := range pkixAttributeTypeNames {
		pkixTypeNameAttributes[value] = key
	}
}

// SequenceSchema is implemented by infoSchema and used by sequence function in expression package.
// Otherwise calling information schema will cause import cycle problem.
type SequenceSchema interface {
	SequenceByName(schema, sequence model.CIStr) (SequenceTable, error)
}

// SequenceTable is implemented by tableCommon, and it is specialised in handling sequence operation.
// Otherwise calling table will cause import cycle problem.
type SequenceTable interface {
	GetSequenceID() int64
	GetSequenceNextVal(ctx interface{}, dbName, seqName string) (int64, error)
	SetSequenceVal(ctx interface{}, newVal int64, dbName, seqName string) (int64, bool, error)
}

// LoadTLSCertificates loads CA/KEY/CERT for special paths.
func LoadTLSCertificates(ca, key, cert string) (tlsConfig *tls.Config, err error) {
	if len(cert) == 0 || len(key) == 0 {
		return
	}

	var tlsCert tls.Certificate
	tlsCert, err = tls.LoadX509KeyPair(cert, key)
	if err != nil {
		logutil.BgLogger().Warn("load x509 failed", zap.Error(err))
		err = errors.Trace(err)
		return
	}

	requireTLS := config.GetGlobalConfig().Security.RequireSecureTransport

	// Try loading CA cert.
	clientAuthPolicy := tls.NoClientCert
	if requireTLS {
		clientAuthPolicy = tls.RequestClientCert
	}
	var certPool *x509.CertPool
	if len(ca) > 0 {
		var caCert []byte
		caCert, err = ioutil.ReadFile(ca)
		if err != nil {
			logutil.BgLogger().Warn("read file failed", zap.Error(err))
			err = errors.Trace(err)
			return
		}
		certPool = x509.NewCertPool()
		if certPool.AppendCertsFromPEM(caCert) {
			if requireTLS {
				clientAuthPolicy = tls.RequireAndVerifyClientCert
			} else {
				clientAuthPolicy = tls.VerifyClientCertIfGiven
			}
		}
	}
	tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		ClientCAs:    certPool,
		ClientAuth:   clientAuthPolicy,
	}
	return
}

// IsTLSExpiredError checks error is caused by TLS expired.
func IsTLSExpiredError(err error) bool {
	err = errors.Cause(err)
	if inval, ok := err.(x509.CertificateInvalidError); !ok || inval.Reason != x509.Expired {
		return false
	}
	return true
}

var (
	internalClientInit sync.Once
	internalHTTPClient *http.Client
	internalHTTPSchema string
)

// InternalHTTPClient is used by TiDB-Server to request other components.
func InternalHTTPClient() *http.Client {
	internalClientInit.Do(initInternalClient)
	return internalHTTPClient
}

// InternalHTTPSchema specifies use http or https to request other components.
func InternalHTTPSchema() string {
	internalClientInit.Do(initInternalClient)
	return internalHTTPSchema
}

func initInternalClient() {
	tlsCfg, err := config.GetGlobalConfig().Security.ToTLSConfig()
	if err != nil {
		logutil.BgLogger().Fatal("could not load cluster ssl", zap.Error(err))
	}
	if tlsCfg == nil {
		internalHTTPSchema = "http"
		internalHTTPClient = http.DefaultClient
		return
	}
	internalHTTPSchema = "https"
	internalHTTPClient = &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
}
