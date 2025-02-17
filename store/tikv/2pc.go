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

package tikv

import (
	"bytes"
	"context"
	"encoding/hex"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/DigitalChinaOpenSource/DCParser/terror"
	"github.com/opentracing/opentracing-go"
	"github.com/pingcap/errors"
	"github.com/pingcap/failpoint"
	pb "github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/metrics"
	"github.com/pingcap/tidb/sessionctx/binloginfo"
	"github.com/pingcap/tidb/store/tikv/oracle"
	"github.com/pingcap/tidb/store/tikv/tikvrpc"
	"github.com/pingcap/tidb/tablecodec"
	"github.com/pingcap/tidb/util/execdetails"
	"github.com/pingcap/tidb/util/logutil"
	"github.com/pingcap/tipb/go-binlog"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

type twoPhaseCommitAction interface {
	handleSingleBatch(*twoPhaseCommitter, *Backoffer, batchMutations) error
	tiKVTxnRegionsNumHistogram() prometheus.Observer
	String() string
}

type actionPrewrite struct{}
type actionCommit struct{ retry bool }
type actionCleanup struct{}
type actionPessimisticLock struct {
	*kv.LockCtx
}
type actionPessimisticRollback struct{}

var (
	_ twoPhaseCommitAction = actionPrewrite{}
	_ twoPhaseCommitAction = actionCommit{}
	_ twoPhaseCommitAction = actionCleanup{}
	_ twoPhaseCommitAction = actionPessimisticLock{}
	_ twoPhaseCommitAction = actionPessimisticRollback{}
)

var (
	tikvSecondaryLockCleanupFailureCounterCommit   = metrics.TiKVSecondaryLockCleanupFailureCounter.WithLabelValues("commit")
	tikvSecondaryLockCleanupFailureCounterRollback = metrics.TiKVSecondaryLockCleanupFailureCounter.WithLabelValues("rollback")
	tiKVTxnHeartBeatHistogramOK                    = metrics.TiKVTxnHeartBeatHistogram.WithLabelValues("ok")
	tiKVTxnHeartBeatHistogramError                 = metrics.TiKVTxnHeartBeatHistogram.WithLabelValues("err")

	tiKVTxnRegionsNumHistogramPrewrite            = metrics.TiKVTxnRegionsNumHistogram.WithLabelValues(metricsTag("prewrite"))
	tiKVTxnRegionsNumHistogramCommit              = metrics.TiKVTxnRegionsNumHistogram.WithLabelValues(metricsTag("commit"))
	tiKVTxnRegionsNumHistogramCleanup             = metrics.TiKVTxnRegionsNumHistogram.WithLabelValues(metricsTag("cleanup"))
	tiKVTxnRegionsNumHistogramPessimisticLock     = metrics.TiKVTxnRegionsNumHistogram.WithLabelValues(metricsTag("pessimistic_lock"))
	tiKVTxnRegionsNumHistogramPessimisticRollback = metrics.TiKVTxnRegionsNumHistogram.WithLabelValues(metricsTag("pessimistic_rollback"))
)

// Global variable set by config file.
var (
	ManagedLockTTL uint64 = 20000 // 20s
)

func (actionPrewrite) String() string {
	return "prewrite"
}

func (actionPrewrite) tiKVTxnRegionsNumHistogram() prometheus.Observer {
	return tiKVTxnRegionsNumHistogramPrewrite
}

func (actionCommit) String() string {
	return "commit"
}

func (actionCommit) tiKVTxnRegionsNumHistogram() prometheus.Observer {
	return tiKVTxnRegionsNumHistogramCommit
}

func (actionCleanup) String() string {
	return "cleanup"
}

func (actionCleanup) tiKVTxnRegionsNumHistogram() prometheus.Observer {
	return tiKVTxnRegionsNumHistogramCleanup
}

func (actionPessimisticLock) String() string {
	return "pessimistic_lock"
}

func (actionPessimisticLock) tiKVTxnRegionsNumHistogram() prometheus.Observer {
	return tiKVTxnRegionsNumHistogramPessimisticLock
}

func (actionPessimisticRollback) String() string {
	return "pessimistic_rollback"
}

func (actionPessimisticRollback) tiKVTxnRegionsNumHistogram() prometheus.Observer {
	return tiKVTxnRegionsNumHistogramPessimisticRollback
}

// metricsTag returns detail tag for metrics.
func metricsTag(action string) string {
	return "2pc_" + action
}

// twoPhaseCommitter executes a two-phase commit protocol.
type twoPhaseCommitter struct {
	store            *tikvStore
	txn              *tikvTxn
	startTS          uint64
	mutations        CommitterMutations
	lockTTL          uint64
	commitTS         uint64
	priority         pb.CommandPri
	connID           uint64 // connID is used for log.
	cleanWg          sync.WaitGroup
	detail           unsafe.Pointer
	txnSize          int
	noNeedCommitKeys map[string]struct{}

	primaryKey  []byte
	forUpdateTS uint64

	mu struct {
		sync.RWMutex
		undeterminedErr error // undeterminedErr saves the rpc error we encounter when commit primary key.
		committed       bool
	}
	syncLog bool
	// For pessimistic transaction
	isPessimistic bool
	isFirstLock   bool
	// regionTxnSize stores the number of keys involved in each region
	regionTxnSize map[uint64]int
	// Used by pessimistic transaction and large transaction.
	ttlManager

	testingKnobs struct {
		acAfterCommitPrimary chan struct{}
		bkAfterCommitPrimary chan struct{}
	}

	// doingAmend means the amend prewrite is ongoing.
	doingAmend bool
}

// CommitterMutations contains transaction operations.
type CommitterMutations struct {
	ops               []pb.Op
	keys              [][]byte
	values            [][]byte
	isPessimisticLock []bool
}

// Mutation represents a single transaction operation.
type Mutation struct {
	KeyOp             pb.Op
	Key               []byte
	Value             []byte
	IsPessimisticLock bool
}

// NewCommiterMutations creates a CommitterMutations object with sizeHint reserved.
func NewCommiterMutations(sizeHint int) CommitterMutations {
	return CommitterMutations{
		ops:               make([]pb.Op, 0, sizeHint),
		keys:              make([][]byte, 0, sizeHint),
		values:            make([][]byte, 0, sizeHint),
		isPessimisticLock: make([]bool, 0, sizeHint),
	}
}

func (c *CommitterMutations) subRange(from, to int) CommitterMutations {
	var res CommitterMutations
	res.keys = c.keys[from:to]
	if c.ops != nil {
		res.ops = c.ops[from:to]
	}
	if c.values != nil {
		res.values = c.values[from:to]
	}
	if c.isPessimisticLock != nil {
		res.isPessimisticLock = c.isPessimisticLock[from:to]
	}
	return res
}

// Push another mutation into mutations.
func (c *CommitterMutations) Push(op pb.Op, key []byte, value []byte, isPessimisticLock bool) {
	c.ops = append(c.ops, op)
	c.keys = append(c.keys, key)
	c.values = append(c.values, value)
	c.isPessimisticLock = append(c.isPessimisticLock, isPessimisticLock)
}

func (c *CommitterMutations) len() int {
	return len(c.keys)
}

// batchExecutor is txn controller providing rate control like utils
type batchExecutor struct {
	rateLim           int                  // concurrent worker numbers
	rateLimiter       *rateLimit           // rate limiter for concurrency control, maybe more strategies
	committer         *twoPhaseCommitter   // here maybe more different type committer in the future
	action            twoPhaseCommitAction // the work action type
	backoffer         *Backoffer           // Backoffer
	tokenWaitDuration time.Duration        // get token wait time
}

// GetKeys returns the keys.
func (c *CommitterMutations) GetKeys() [][]byte {
	return c.keys
}

// GetOps returns the key ops.
func (c *CommitterMutations) GetOps() []pb.Op {
	return c.ops
}

// GetValues returns the key values.
func (c *CommitterMutations) GetValues() [][]byte {
	return c.values
}

// GetPessimisticFlags returns the key pessimistic flags.
func (c *CommitterMutations) GetPessimisticFlags() []bool {
	return c.isPessimisticLock
}

// MergeMutations append input mutations into current mutations.
func (c *CommitterMutations) MergeMutations(mutations CommitterMutations) {
	c.ops = append(c.ops, mutations.ops...)
	c.keys = append(c.keys, mutations.keys...)
	c.values = append(c.values, mutations.values...)
	c.isPessimisticLock = append(c.isPessimisticLock, mutations.isPessimisticLock...)
}

// AppendMutation merges a single Mutation into the current mutations.
func (c *CommitterMutations) AppendMutation(mutation Mutation) {
	c.ops = append(c.ops, mutation.KeyOp)
	c.keys = append(c.keys, mutation.Key)
	c.values = append(c.values, mutation.Value)
	c.isPessimisticLock = append(c.isPessimisticLock, mutation.IsPessimisticLock)
}

// newTwoPhaseCommitter creates a twoPhaseCommitter.
func newTwoPhaseCommitter(txn *tikvTxn, connID uint64) (*twoPhaseCommitter, error) {
	return &twoPhaseCommitter{
		store:         txn.store,
		txn:           txn,
		startTS:       txn.StartTS(),
		connID:        connID,
		regionTxnSize: map[uint64]int{},
		ttlManager: ttlManager{
			ch: make(chan struct{}),
		},
		isPessimistic: txn.IsPessimistic(),
	}, nil
}

func sendTxnHeartBeat(bo *Backoffer, store *tikvStore, primary []byte, startTS, ttl uint64) (uint64, error) {
	req := tikvrpc.NewRequest(tikvrpc.CmdTxnHeartBeat, &pb.TxnHeartBeatRequest{
		PrimaryLock:   primary,
		StartVersion:  startTS,
		AdviseLockTtl: ttl,
	})
	for {
		loc, err := store.GetRegionCache().LocateKey(bo, primary)
		if err != nil {
			return 0, errors.Trace(err)
		}
		resp, err := store.SendReq(bo, req, loc.Region, readTimeoutShort)
		if err != nil {
			return 0, errors.Trace(err)
		}
		regionErr, err := resp.GetRegionError()
		if err != nil {
			return 0, errors.Trace(err)
		}
		if regionErr != nil {
			err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
			if err != nil {
				return 0, errors.Trace(err)
			}
			continue
		}
		if resp.Resp == nil {
			return 0, errors.Trace(ErrBodyMissing)
		}
		cmdResp := resp.Resp.(*pb.TxnHeartBeatResponse)
		if keyErr := cmdResp.GetError(); keyErr != nil {
			return 0, errors.Errorf("txn %d heartbeat fail, primary key = %v, err = %s", startTS, primary, keyErr.Abort)
		}
		return cmdResp.GetLockTtl(), nil
	}
}

func (c *twoPhaseCommitter) initKeysAndMutations() error {
	var (
		size            int
		putCnt          int
		delCnt          int
		lockCnt         int
		noNeedCommitKey = make(map[string]struct{})
	)
	txn := c.txn
	sizeHint := len(txn.lockKeys) + txn.us.Len()
	mutations := NewCommiterMutations(sizeHint)
	c.isPessimistic = txn.IsPessimistic()

	// Merge ordered lockKeys and pairs in the memBuffer into the mutations array
	sort.Slice(txn.lockKeys, func(i, j int) bool {
		return bytes.Compare(txn.lockKeys[i], txn.lockKeys[j]) < 0
	})
	lockIdx := 0
	err := txn.us.WalkBuffer(func(k kv.Key, v []byte) error {
		var (
			op                pb.Op
			value             []byte
			isPessimisticLock bool
		)
		if len(v) > 0 {
			if tablecodec.IsUntouchedIndexKValue(k, v) {
				if _, ok := c.txn.lockedMap[string(k)]; !ok {
					return nil
				}
				op = pb.Op_Lock
				value = v
				lockCnt++
			} else {
				op = pb.Op_Put
				if c := txn.us.GetKeyExistErrInfo(k); c != nil {
					op = pb.Op_Insert
				}
				value = v
				putCnt++
			}
		} else {
			if !txn.IsPessimistic() && txn.us.GetKeyExistErrInfo(k) != nil {
				// delete-your-writes keys in optimistic txn need check not exists in prewrite-phase
				// due to `Op_CheckNotExists` doesn't prewrite lock, so mark those keys should not be used in commit-phase.
				op = pb.Op_CheckNotExists
				noNeedCommitKey[string(k)] = struct{}{}
			} else {
				// normal delete keys in optimistic txn can be delete without not exists checking
				// delete-your-writes keys in pessimistic txn can ensure must be no exists so can directly delete them
				op = pb.Op_Del
				delCnt++
			}
		}
		for lockIdx < len(txn.lockKeys) {
			lockKey := txn.lockKeys[lockIdx]
			ord := bytes.Compare(lockKey, k)
			if ord == 0 {
				isPessimisticLock = c.isPessimistic
				lockIdx++
				break
			} else if ord > 0 {
				break
			} else {
				mutations.Push(pb.Op_Lock, lockKey, nil, c.isPessimistic)
				lockCnt++
				size += len(lockKey)
				lockIdx++
			}
		}
		mutations.Push(op, k, value, isPessimisticLock)
		entrySize := len(k) + len(v)
		if uint64(entrySize) > kv.TxnEntrySizeLimit {
			return kv.ErrEntryTooLarge.GenWithStackByArgs(kv.TxnEntrySizeLimit, entrySize)
		}
		size += entrySize
		return nil
	})
	if err != nil {
		return errors.Trace(err)
	}
	// add the remaining locks to mutations and keys
	for _, lockKey := range txn.lockKeys[lockIdx:] {
		mutations.Push(pb.Op_Lock, lockKey, nil, c.isPessimistic)
		lockCnt++
		size += len(lockKey)
	}
	if mutations.len() == 0 {
		return nil
	}
	c.txnSize = size

	if len(c.primaryKey) == 0 {
		for i, op := range mutations.ops {
			if op != pb.Op_CheckNotExists {
				c.primaryKey = mutations.keys[i]
				break
			}
		}
	}

	if size > int(kv.TxnTotalSizeLimit) {
		return kv.ErrTxnTooLarge.GenWithStackByArgs(size)
	}
	const logEntryCount = 10000
	const logSize = 4 * 1024 * 1024 // 4MB
	if mutations.len() > logEntryCount || size > logSize {
		tableID := tablecodec.DecodeTableID(mutations.keys[0])
		logutil.BgLogger().Info("[BIG_TXN]",
			zap.Uint64("con", c.connID),
			zap.Int64("table ID", tableID),
			zap.Int("size", size),
			zap.Int("keys", mutations.len()),
			zap.Int("puts", putCnt),
			zap.Int("dels", delCnt),
			zap.Int("locks", lockCnt),
			zap.Int("checks", len(noNeedCommitKey)),
			zap.Uint64("txnStartTS", txn.startTS))
	}

	// Sanity check for startTS.
	if txn.StartTS() == math.MaxUint64 {
		err = errors.Errorf("try to commit with invalid txnStartTS: %d", txn.StartTS())
		logutil.BgLogger().Error("commit failed",
			zap.Uint64("conn", c.connID),
			zap.Error(err))
		return errors.Trace(err)
	}

	commitDetail := &execdetails.CommitDetails{WriteSize: size, WriteKeys: mutations.len()}
	metrics.TiKVTxnWriteKVCountHistogram.Observe(float64(commitDetail.WriteKeys))
	metrics.TiKVTxnWriteSizeHistogram.Observe(float64(commitDetail.WriteSize))
	c.noNeedCommitKeys = noNeedCommitKey
	c.mutations = mutations
	c.lockTTL = txnLockTTL(txn.startTime, size)
	c.priority = getTxnPriority(txn)
	c.syncLog = getTxnSyncLog(txn)
	c.setDetail(commitDetail)
	return nil
}

func (c *twoPhaseCommitter) primary() []byte {
	if len(c.primaryKey) == 0 {
		return c.mutations.keys[0]
	}
	return c.primaryKey
}

const bytesPerMiB = 1024 * 1024

func txnLockTTL(startTime time.Time, txnSize int) uint64 {
	// Increase lockTTL for large transactions.
	// The formula is `ttl = ttlFactor * sqrt(sizeInMiB)`.
	// When writeSize is less than 256KB, the base ttl is defaultTTL (3s);
	// When writeSize is 1MiB, 4MiB, or 10MiB, ttl is 6s, 12s, 20s correspondingly;
	lockTTL := defaultLockTTL
	if txnSize >= txnCommitBatchSize {
		sizeMiB := float64(txnSize) / bytesPerMiB
		lockTTL = uint64(float64(ttlFactor) * math.Sqrt(sizeMiB))
		if lockTTL < defaultLockTTL {
			lockTTL = defaultLockTTL
		}
		if lockTTL > ManagedLockTTL {
			lockTTL = ManagedLockTTL
		}
	}

	// Increase lockTTL by the transaction's read time.
	// When resolving a lock, we compare current ts and startTS+lockTTL to decide whether to clean up. If a txn
	// takes a long time to read, increasing its TTL will help to prevent it from been aborted soon after prewrite.
	elapsed := time.Since(startTime) / time.Millisecond
	return lockTTL + uint64(elapsed)
}

var preSplitDetectThreshold uint32 = 100000
var preSplitSizeThreshold uint32 = 32 << 20

// doActionOnMutations groups keys into primary batch and secondary batches, if primary batch exists in the key,
// it does action on primary batch first, then on secondary batches. If action is commit, secondary batches
// is done in background goroutine.
func (c *twoPhaseCommitter) doActionOnMutations(bo *Backoffer, action twoPhaseCommitAction, mutations CommitterMutations) error {
	if mutations.len() == 0 {
		return nil
	}
	groups, err := c.store.regionCache.GroupSortedMutationsByRegion(bo, mutations)
	if err != nil {
		return errors.Trace(err)
	}

	// Pre-split regions to avoid too much write workload into a single region.
	// In the large transaction case, this operation is important to avoid TiKV 'server is busy' error.
	var preSplited bool
	preSplitDetectThresholdVal := atomic.LoadUint32(&preSplitDetectThreshold)
	for _, group := range groups {
		if uint32(group.mutations.len()) >= preSplitDetectThresholdVal {
			logutil.BgLogger().Info("2PC detect large amount of mutations on a single region",
				zap.Uint64("region", group.region.GetID()),
				zap.Int("mutations count", group.mutations.len()))
			// Use context.Background, this time should not add up to Backoffer.
			if preSplitAndScatterIn2PC(context.Background(), c.store, group) {
				preSplited = true
			}
		}
	}
	// Reload region cache again.
	if preSplited {
		groups, err = c.store.regionCache.GroupSortedMutationsByRegion(bo, mutations)
		if err != nil {
			return errors.Trace(err)
		}
	}

	return c.doActionOnGroupMutations(bo, action, groups)
}

func preSplitAndScatterIn2PC(ctx context.Context, store *tikvStore, group groupedMutations) bool {
	splitKeys := make([][]byte, 0, 4)

	preSplitSizeThresholdVal := atomic.LoadUint32(&preSplitSizeThreshold)
	regionSize := 0
	keysLength := group.mutations.len()
	valsLength := len(group.mutations.values)
	// The value length maybe zero for pessimistic lock keys
	for i := 0; i < keysLength; i++ {
		regionSize = regionSize + len(group.mutations.keys[i])
		if i < valsLength {
			regionSize = regionSize + len(group.mutations.values[i])
		}
		// The second condition is used for testing.
		if regionSize >= int(preSplitSizeThresholdVal) {
			regionSize = 0
			splitKeys = append(splitKeys, group.mutations.keys[i])
		}
	}
	if len(splitKeys) == 0 {
		return false
	}

	regionIDs, err := store.SplitRegions(ctx, splitKeys, true, nil)
	if err != nil {
		logutil.BgLogger().Warn("2PC split regions failed", zap.Uint64("regionID", group.region.id),
			zap.Int("keys count", keysLength), zap.Int("values count", valsLength), zap.Error(err))
		return false
	}

	for _, regionID := range regionIDs {
		err := store.WaitScatterRegionFinish(ctx, regionID, 0)
		if err != nil {
			logutil.BgLogger().Warn("2PC wait scatter region failed", zap.Uint64("regionID", regionID), zap.Error(err))
		}
	}
	// Invalidate the old region cache information.
	store.regionCache.InvalidateCachedRegion(group.region)
	return true
}

func (c *twoPhaseCommitter) doActionOnGroupMutations(bo *Backoffer, action twoPhaseCommitAction, groups []groupedMutations) error {
	action.tiKVTxnRegionsNumHistogram().Observe(float64(len(groups)))

	var batches []batchMutations
	var sizeFunc = c.keySize

	switch act := action.(type) {
	case actionPrewrite:
		// Do not update regionTxnSize on retries. They are not used when building a PrewriteRequest.
		if len(bo.errors) == 0 {
			for _, group := range groups {
				c.regionTxnSize[group.region.id] = group.mutations.len()
			}
		}
		sizeFunc = c.keyValueSize
		atomic.AddInt32(&c.getDetail().PrewriteRegionNum, int32(len(groups)))
	case actionPessimisticLock:
		if act.LockCtx.Stats != nil {
			act.LockCtx.Stats.RegionNum = int32(len(groups))
		}
	}

	primaryIdx := -1
	for _, group := range groups {
		batches = c.appendBatchMutationsBySize(batches, group.region, group.mutations, sizeFunc, txnCommitBatchSize, &primaryIdx)
	}

	firstIsPrimary := false
	// If the batches include the primary key, put it to the first
	if primaryIdx >= 0 {
		batches[primaryIdx].isPrimary = true
		batches[0], batches[primaryIdx] = batches[primaryIdx], batches[0]
		firstIsPrimary = true
	}

	actionCommit, actionIsCommit := action.(actionCommit)
	_, actionIsCleanup := action.(actionCleanup)
	_, actionIsPessimiticLock := action.(actionPessimisticLock)

	var err error
	failpoint.Inject("skipKeyReturnOK", func(val failpoint.Value) {
		valStr, ok := val.(string)
		if ok && c.connID > 0 {
			if firstIsPrimary && actionIsPessimiticLock {
				logutil.Logger(bo.ctx).Warn("pessimisticLock failpoint", zap.String("valStr", valStr))
				switch valStr {
				case "pessimisticLockSkipPrimary":
					err = c.doActionOnBatches(bo, action, batches)
					failpoint.Return(err)
				case "pessimisticLockSkipSecondary":
					err = c.doActionOnBatches(bo, action, batches[:1])
					failpoint.Return(err)
				}
			}
		}
	})
	failpoint.Inject("pessimisticRollbackDoNth", func() {
		_, actionIsPessimisticRollback := action.(actionPessimisticRollback)
		if actionIsPessimisticRollback && c.connID > 0 {
			logutil.Logger(bo.ctx).Warn("pessimisticRollbackDoNth failpoint")
			failpoint.Return(nil)
		}
	})

	if firstIsPrimary && (actionIsCommit || actionIsCleanup || actionIsPessimiticLock) {
		// primary should be committed/cleanup/pessimistically locked first
		err = c.doActionOnBatches(bo, action, batches[:1])
		if err != nil {
			return errors.Trace(err)
		}
		if actionIsCommit && c.testingKnobs.bkAfterCommitPrimary != nil && c.testingKnobs.acAfterCommitPrimary != nil {
			c.testingKnobs.acAfterCommitPrimary <- struct{}{}
			<-c.testingKnobs.bkAfterCommitPrimary
		}
		batches = batches[1:]
	}
	if actionIsCommit && !actionCommit.retry {
		// Commit secondary batches in background goroutine to reduce latency.
		// The backoffer instance is created outside of the goroutine to avoid
		// potential data race in unit test since `CommitMaxBackoff` will be updated
		// by test suites.
		secondaryBo := NewBackofferWithVars(context.Background(), CommitMaxBackoff, c.txn.vars)
		go func() {
			if c.connID > 0 {
				failpoint.Inject("beforeCommitSecondaries", func(v failpoint.Value) {
					if s, ok := v.(string); !ok {
						logutil.Logger(bo.ctx).Info("[failpoint] sleep 2s before commit secondary keys",
							zap.Uint64("connID", c.connID), zap.Uint64("txnStartTS", c.startTS), zap.Uint64("txnCommitTS", c.commitTS))
						time.Sleep(2 * time.Second)
					} else if s == "skip" {
						logutil.Logger(bo.ctx).Info("[failpoint] injected skip committing secondaries",
							zap.Uint64("connID", c.connID), zap.Uint64("txnStartTS", c.startTS), zap.Uint64("txnCommitTS", c.commitTS))
						failpoint.Return()
					}
				})
			}

			e := c.doActionOnBatches(secondaryBo, action, batches)
			if e != nil {
				logutil.BgLogger().Debug("2PC async doActionOnBatches",
					zap.Uint64("conn", c.connID),
					zap.Stringer("action type", action),
					zap.Error(e))
				tikvSecondaryLockCleanupFailureCounterCommit.Inc()
			}
		}()
	} else {
		err = c.doActionOnBatches(bo, action, batches)
	}
	return errors.Trace(err)
}

// doActionOnBatches does action to batches in parallel.
func (c *twoPhaseCommitter) doActionOnBatches(bo *Backoffer, action twoPhaseCommitAction, batches []batchMutations) error {
	if len(batches) == 0 {
		return nil
	}

	noNeedFork := len(batches) == 1
	if !noNeedFork {
		if ac, ok := action.(actionCommit); ok && ac.retry {
			noNeedFork = true
		}
	}
	if noNeedFork {
		for _, b := range batches {
			e := action.handleSingleBatch(c, bo, b)
			if e != nil {
				logutil.BgLogger().Debug("2PC doActionOnBatches failed",
					zap.Uint64("conn", c.connID),
					zap.Stringer("action type", action),
					zap.Error(e),
					zap.Uint64("txnStartTS", c.startTS))
				return errors.Trace(e)
			}
		}
		return nil
	}
	rateLim := len(batches)
	// Set rateLim here for the large transaction.
	// If the rate limit is too high, tikv will report service is busy.
	// If the rate limit is too low, we can't full utilize the tikv's throughput.
	// TODO: Find a self-adaptive way to control the rate limit here.
	if rateLim > config.GetGlobalConfig().Performance.CommitterConcurrency {
		rateLim = config.GetGlobalConfig().Performance.CommitterConcurrency
	}
	batchExecutor := newBatchExecutor(rateLim, c, action, bo)
	err := batchExecutor.process(batches)
	return errors.Trace(err)
}

func (c *twoPhaseCommitter) keyValueSize(key, value []byte) int {
	return len(key) + len(value)
}

func (c *twoPhaseCommitter) keySize(key, value []byte) int {
	return len(key)
}

func (c *twoPhaseCommitter) buildPrewriteRequest(batch batchMutations, txnSize uint64) *tikvrpc.Request {
	m := &batch.mutations
	mutations := make([]*pb.Mutation, m.len())
	for i := range m.keys {
		mutations[i] = &pb.Mutation{
			Op:    m.ops[i],
			Key:   m.keys[i],
			Value: m.values[i],
		}
	}
	var minCommitTS uint64
	if c.forUpdateTS > 0 {
		minCommitTS = c.forUpdateTS + 1
	} else {
		minCommitTS = c.startTS + 1
	}

	failpoint.Inject("mockZeroCommitTS", func(val failpoint.Value) {
		// Should be val.(uint64) but failpoint doesn't support that.
		if tmp, ok := val.(int); ok && uint64(tmp) == c.startTS {
			minCommitTS = 0
		}
	})

	ttl := c.lockTTL

	if c.connID > 0 {
		failpoint.Inject("twoPCShortLockTTL", func() {
			ttl = 1
			keys := make([]string, 0, len(mutations))
			for _, m := range mutations {
				keys = append(keys, hex.EncodeToString(m.Key))
			}
			logutil.BgLogger().Info("[failpoint] injected lock ttl = 1 on prewrite",
				zap.Uint64("txnStartTS", c.startTS), zap.Strings("keys", keys))
		})
	}

	req := &pb.PrewriteRequest{
		Mutations:         mutations,
		PrimaryLock:       c.primary(),
		StartVersion:      c.startTS,
		LockTtl:           ttl,
		IsPessimisticLock: m.isPessimisticLock,
		ForUpdateTs:       c.forUpdateTS,
		TxnSize:           txnSize,
		MinCommitTs:       minCommitTS,
	}
	return tikvrpc.NewRequest(tikvrpc.CmdPrewrite, req, pb.Context{Priority: c.priority, SyncLog: c.syncLog})
}

func (actionPrewrite) handleSingleBatch(c *twoPhaseCommitter, bo *Backoffer, batch batchMutations) error {
	if c.connID > 0 {
		failpoint.Inject("prewritePrimaryFail", func() {
			if batch.isPrimary {
				logutil.Logger(bo.ctx).Info("[failpoint] injected error on prewriting primary batch",
					zap.Uint64("txnStartTS", c.startTS))
				failpoint.Return(errors.New("injected error on prewriting primary batch"))
			}
		})
	}

	txnSize := uint64(c.regionTxnSize[batch.region.id])
	// When we retry because of a region miss, we don't know the transaction size. We set the transaction size here
	// to MaxUint64 to avoid unexpected "resolve lock lite".
	if len(bo.errors) > 0 {
		txnSize = math.MaxUint64
	}

	req := c.buildPrewriteRequest(batch, txnSize)
	for {
		resp, err := c.store.SendReq(bo, req, batch.region, readTimeoutShort)
		if err != nil {
			return errors.Trace(err)
		}
		regionErr, err := resp.GetRegionError()
		if err != nil {
			return errors.Trace(err)
		}
		if regionErr != nil {
			err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
			if err != nil {
				return errors.Trace(err)
			}
			err = c.prewriteMutations(bo, batch.mutations)
			return errors.Trace(err)
		}
		if resp.Resp == nil {
			return errors.Trace(ErrBodyMissing)
		}
		prewriteResp := resp.Resp.(*pb.PrewriteResponse)
		keyErrs := prewriteResp.GetErrors()
		if len(keyErrs) == 0 {
			if batch.isPrimary {
				// After writing the primary key, if the size of the transaction is large than 32M,
				// start the ttlManager. The ttlManager will be closed in tikvTxn.Commit().
				if c.txnSize > 32*1024*1024 {
					c.run(c, nil)
				}
			}
			return nil
		}
		var locks []*Lock
		for _, keyErr := range keyErrs {
			// Check already exists error
			if alreadyExist := keyErr.GetAlreadyExist(); alreadyExist != nil {
				key := alreadyExist.GetKey()
				existErrInfo := c.txn.us.GetKeyExistErrInfo(key)
				if existErrInfo == nil {
					return errors.Errorf("conn %d, existErr for key:%s should not be nil", c.connID, key)
				}
				return existErrInfo.Err()
			}

			// Extract lock from key error
			lock, err1 := extractLockFromKeyErr(keyErr)
			if err1 != nil {
				return errors.Trace(err1)
			}
			logutil.BgLogger().Info("prewrite encounters lock",
				zap.Uint64("conn", c.connID),
				zap.Stringer("lock", lock))
			locks = append(locks, lock)
		}
		start := time.Now()
		msBeforeExpired, err := c.store.lockResolver.resolveLocksForWrite(bo, c.startTS, locks)
		if err != nil {
			return errors.Trace(err)
		}
		atomic.AddInt64(&c.getDetail().ResolveLockTime, int64(time.Since(start)))
		if msBeforeExpired > 0 {
			err = bo.BackoffWithMaxSleep(BoTxnLock, int(msBeforeExpired), errors.Errorf("2PC prewrite lockedKeys: %d", len(locks)))
			if err != nil {
				return errors.Trace(err)
			}
		}
	}
}

type ttlManagerState uint32

const (
	stateUninitialized ttlManagerState = iota
	stateRunning
	stateClosed
)

type ttlManager struct {
	state   ttlManagerState
	ch      chan struct{}
	lockCtx *kv.LockCtx
}

func (tm *ttlManager) run(c *twoPhaseCommitter, lockCtx *kv.LockCtx) {
	// Run only once.
	if !atomic.CompareAndSwapUint32((*uint32)(&tm.state), uint32(stateUninitialized), uint32(stateRunning)) {
		return
	}
	tm.lockCtx = lockCtx
	go tm.keepAlive(c)
}

func (tm *ttlManager) close() {
	if !atomic.CompareAndSwapUint32((*uint32)(&tm.state), uint32(stateRunning), uint32(stateClosed)) {
		return
	}
	close(tm.ch)
}

func (tm *ttlManager) keepAlive(c *twoPhaseCommitter) {
	// Ticker is set to 1/2 of the ManagedLockTTL.
	ticker := time.NewTicker(time.Duration(atomic.LoadUint64(&ManagedLockTTL)) * time.Millisecond / 2)
	defer ticker.Stop()
	for {
		select {
		case <-tm.ch:
			return
		case <-ticker.C:
			// If kill signal is received, the ttlManager should exit.
			if tm.lockCtx != nil && tm.lockCtx.Killed != nil && atomic.LoadUint32(tm.lockCtx.Killed) != 0 {
				return
			}
			bo := NewBackofferWithVars(context.Background(), pessimisticLockMaxBackoff, c.txn.vars)
			now, err := c.store.GetOracle().GetTimestamp(bo.ctx)
			if err != nil {
				err1 := bo.Backoff(BoPDRPC, err)
				if err1 != nil {
					logutil.Logger(bo.ctx).Warn("keepAlive get tso fail",
						zap.Error(err))
					return
				}
				continue
			}

			uptime := uint64(oracle.ExtractPhysical(now) - oracle.ExtractPhysical(c.startTS))
			if uptime > config.GetGlobalConfig().Performance.MaxTxnTTL {
				// Checks maximum lifetime for the ttlManager, so when something goes wrong
				// the key will not be locked forever.
				logutil.Logger(bo.ctx).Info("ttlManager live up to its lifetime",
					zap.Uint64("txnStartTS", c.startTS),
					zap.Uint64("uptime", uptime),
					zap.Uint64("maxTxnTTL", config.GetGlobalConfig().Performance.MaxTxnTTL))
				metrics.TiKVTTLLifeTimeReachCounter.Inc()
				// the pessimistic locks may expire if the ttl manager has timed out, set `LockExpired` flag
				// so that this transaction could only commit or rollback with no more statement executions
				if c.isPessimistic && tm.lockCtx != nil && tm.lockCtx.LockExpired != nil {
					atomic.StoreUint32(tm.lockCtx.LockExpired, 1)
				}
				return
			}

			newTTL := uptime + atomic.LoadUint64(&ManagedLockTTL)
			logutil.Logger(bo.ctx).Info("send TxnHeartBeat",
				zap.Uint64("startTS", c.startTS), zap.Uint64("newTTL", newTTL))
			startTime := time.Now()
			_, err = sendTxnHeartBeat(bo, c.store, c.primary(), c.startTS, newTTL)
			if err != nil {
				tiKVTxnHeartBeatHistogramError.Observe(time.Since(startTime).Seconds())
				logutil.Logger(bo.ctx).Warn("send TxnHeartBeat failed",
					zap.Error(err),
					zap.Uint64("txnStartTS", c.startTS))
				return
			}
			tiKVTxnHeartBeatHistogramOK.Observe(time.Since(startTime).Seconds())
		}
	}
}

func (action actionPessimisticLock) handleSingleBatch(c *twoPhaseCommitter, bo *Backoffer, batch batchMutations) error {
	m := &batch.mutations
	mutations := make([]*pb.Mutation, m.len())
	for i := range m.keys {
		mut := &pb.Mutation{
			Op:  pb.Op_PessimisticLock,
			Key: m.keys[i],
		}
		existErr := c.txn.us.GetKeyExistErrInfo(m.keys[i])
		if existErr != nil || (c.doingAmend && m.GetOps()[i] == pb.Op_Insert) {
			mut.Assertion = pb.Assertion_NotExist
		}
		mutations[i] = mut
	}
	elapsed := uint64(time.Since(c.txn.startTime) / time.Millisecond)
	req := tikvrpc.NewRequest(tikvrpc.CmdPessimisticLock, &pb.PessimisticLockRequest{
		Mutations:    mutations,
		PrimaryLock:  c.primary(),
		StartVersion: c.startTS,
		ForUpdateTs:  c.forUpdateTS,
		LockTtl:      elapsed + atomic.LoadUint64(&ManagedLockTTL),
		IsFirstLock:  c.isFirstLock,
		WaitTimeout:  action.LockWaitTime,
		ReturnValues: action.ReturnValues,
		MinCommitTs:  c.forUpdateTS + 1,
	}, pb.Context{Priority: c.priority, SyncLog: c.syncLog})
	lockWaitStartTime := action.WaitStartTime
	for {
		// if lockWaitTime set, refine the request `WaitTimeout` field based on timeout limit
		if action.LockWaitTime > 0 {
			timeLeft := action.LockWaitTime - (time.Since(lockWaitStartTime)).Milliseconds()
			if timeLeft <= 0 {
				req.PessimisticLock().WaitTimeout = kv.LockNoWait
			} else {
				req.PessimisticLock().WaitTimeout = timeLeft
			}
		}
		failpoint.Inject("PessimisticLockErrWriteConflict", func() error {
			time.Sleep(300 * time.Millisecond)
			return kv.ErrWriteConflict
		})
		startTime := time.Now()
		resp, err := c.store.SendReq(bo, req, batch.region, readTimeoutShort)
		if action.LockCtx.Stats != nil {
			atomic.AddInt64(&action.LockCtx.Stats.LockRPCTime, int64(time.Since(startTime)))
			atomic.AddInt64(&action.LockCtx.Stats.LockRPCCount, 1)
		}
		if err != nil {
			return errors.Trace(err)
		}
		regionErr, err := resp.GetRegionError()
		if err != nil {
			return errors.Trace(err)
		}
		if regionErr != nil {
			err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
			if err != nil {
				return errors.Trace(err)
			}
			err = c.pessimisticLockMutations(bo, action.LockCtx, batch.mutations)
			return errors.Trace(err)
		}
		if resp.Resp == nil {
			return errors.Trace(ErrBodyMissing)
		}
		lockResp := resp.Resp.(*pb.PessimisticLockResponse)
		keyErrs := lockResp.GetErrors()
		if len(keyErrs) == 0 {
			if action.ReturnValues {
				action.ValuesLock.Lock()
				for i, mutation := range mutations {
					action.Values[string(mutation.Key)] = kv.ReturnedValue{Value: lockResp.Values[i]}
				}
				action.ValuesLock.Unlock()
			}
			return nil
		}
		var locks []*Lock
		for _, keyErr := range keyErrs {
			// Check already exists error
			if alreadyExist := keyErr.GetAlreadyExist(); alreadyExist != nil {
				key := alreadyExist.GetKey()
				existErrInfo := c.txn.us.GetKeyExistErrInfo(key)
				if existErrInfo == nil {
					return errors.Errorf("conn %d, existErr for key:%s should not be nil", c.connID, key)
				}
				return existErrInfo.Err()
			}
			if deadlock := keyErr.Deadlock; deadlock != nil {
				return &ErrDeadlock{Deadlock: deadlock}
			}

			// Extract lock from key error
			lock, err1 := extractLockFromKeyErr(keyErr)
			if err1 != nil {
				return errors.Trace(err1)
			}
			locks = append(locks, lock)
		}
		// Because we already waited on tikv, no need to Backoff here.
		// tikv default will wait 3s(also the maximum wait value) when lock error occurs
		startTime = time.Now()
		msBeforeTxnExpired, _, err := c.store.lockResolver.ResolveLocks(bo, 0, locks)
		if action.LockCtx.Stats != nil {
			atomic.AddInt64(&action.LockCtx.Stats.ResolveLockTime, int64(time.Since(startTime)))
		}
		if err != nil {
			return errors.Trace(err)
		}

		// If msBeforeTxnExpired is not zero, it means there are still locks blocking us acquiring
		// the pessimistic lock. We should return acquire fail with nowait set or timeout error if necessary.
		if msBeforeTxnExpired > 0 {
			if action.LockWaitTime == kv.LockNoWait {
				return ErrLockAcquireFailAndNoWaitSet
			} else if action.LockWaitTime == kv.LockAlwaysWait {
				// do nothing but keep wait
			} else {
				// the lockWaitTime is set, we should return wait timeout if we are still blocked by a lock
				if time.Since(lockWaitStartTime).Milliseconds() >= action.LockWaitTime {
					return errors.Trace(ErrLockWaitTimeout)
				}
			}
			if action.LockCtx.PessimisticLockWaited != nil {
				atomic.StoreInt32(action.LockCtx.PessimisticLockWaited, 1)
			}
		}

		// Handle the killed flag when waiting for the pessimistic lock.
		// When a txn runs into LockKeys() and backoff here, it has no chance to call
		// executor.Next() and check the killed flag.
		if action.Killed != nil {
			// Do not reset the killed flag here!
			// actionPessimisticLock runs on each region parallelly, we have to consider that
			// the error may be dropped.
			if atomic.LoadUint32(action.Killed) == 1 {
				return errors.Trace(ErrQueryInterrupted)
			}
		}
	}
}

func (actionPessimisticRollback) handleSingleBatch(c *twoPhaseCommitter, bo *Backoffer, batch batchMutations) error {
	req := tikvrpc.NewRequest(tikvrpc.CmdPessimisticRollback, &pb.PessimisticRollbackRequest{
		StartVersion: c.startTS,
		ForUpdateTs:  c.forUpdateTS,
		Keys:         batch.mutations.keys,
	})
	resp, err := c.store.SendReq(bo, req, batch.region, readTimeoutShort)
	if err != nil {
		return errors.Trace(err)
	}
	regionErr, err := resp.GetRegionError()
	if err != nil {
		return errors.Trace(err)
	}
	if regionErr != nil {
		err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
		if err != nil {
			return errors.Trace(err)
		}
		err = c.pessimisticRollbackMutations(bo, batch.mutations)
		return errors.Trace(err)
	}
	return nil
}

func getTxnPriority(txn *tikvTxn) pb.CommandPri {
	if pri := txn.us.GetOption(kv.Priority); pri != nil {
		return kvPriorityToCommandPri(pri.(int))
	}
	return pb.CommandPri_Normal
}

func getTxnSyncLog(txn *tikvTxn) bool {
	if syncOption := txn.us.GetOption(kv.SyncLog); syncOption != nil {
		return syncOption.(bool)
	}
	return false
}

func kvPriorityToCommandPri(pri int) pb.CommandPri {
	switch pri {
	case kv.PriorityLow:
		return pb.CommandPri_Low
	case kv.PriorityHigh:
		return pb.CommandPri_High
	default:
		return pb.CommandPri_Normal
	}
}

func (c *twoPhaseCommitter) setDetail(d *execdetails.CommitDetails) {
	atomic.StorePointer(&c.detail, unsafe.Pointer(d))
}

func (c *twoPhaseCommitter) getDetail() *execdetails.CommitDetails {
	return (*execdetails.CommitDetails)(atomic.LoadPointer(&c.detail))
}

func (c *twoPhaseCommitter) setUndeterminedErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.undeterminedErr = err
}

func (c *twoPhaseCommitter) getUndeterminedErr() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mu.undeterminedErr
}

func (actionCommit) handleSingleBatch(c *twoPhaseCommitter, bo *Backoffer, batch batchMutations) error {
	req := tikvrpc.NewRequest(tikvrpc.CmdCommit, &pb.CommitRequest{
		StartVersion:  c.startTS,
		Keys:          batch.mutations.keys,
		CommitVersion: c.commitTS,
	}, pb.Context{Priority: c.priority, SyncLog: c.syncLog})

	sender := NewRegionRequestSender(c.store.regionCache, c.store.client)
	resp, err := sender.SendReq(bo, req, batch.region, readTimeoutShort)

	// If we fail to receive response for the request that commits primary key, it will be undetermined whether this
	// transaction has been successfully committed.
	// Under this circumstance,  we can not declare the commit is complete (may lead to data lost), nor can we throw
	// an error (may lead to the duplicated key error when upper level restarts the transaction). Currently the best
	// solution is to populate this error and let upper layer drop the connection to the corresponding mysql client.
	if batch.isPrimary && sender.rpcError != nil {
		c.setUndeterminedErr(errors.Trace(sender.rpcError))
	}

	if err != nil {
		return errors.Trace(err)
	}
	regionErr, err := resp.GetRegionError()
	if err != nil {
		return errors.Trace(err)
	}
	if regionErr != nil {
		err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
		if err != nil {
			return errors.Trace(err)
		}
		// re-split keys and commit again.
		err = c.doActionOnMutations(bo, actionCommit{retry: true}, batch.mutations)
		return errors.Trace(err)
	}
	if resp.Resp == nil {
		return errors.Trace(ErrBodyMissing)
	}
	commitResp := resp.Resp.(*pb.CommitResponse)
	// Here we can make sure tikv has processed the commit primary key request. So
	// we can clean undetermined error.
	if batch.isPrimary {
		c.setUndeterminedErr(nil)
	}
	if keyErr := commitResp.GetError(); keyErr != nil {
		if rejected := keyErr.GetCommitTsExpired(); rejected != nil {
			logutil.Logger(bo.ctx).Info("2PC commitTS rejected by TiKV, retry with a newer commitTS",
				zap.Uint64("txnStartTS", c.startTS),
				zap.Stringer("info", logutil.Hex(rejected)))

			// Do not retry for a txn which has a too large MinCommitTs
			// 3600000 << 18 = 943718400000
			if rejected.MinCommitTs-rejected.AttemptedCommitTs > 943718400000 {
				err := errors.Errorf("2PC MinCommitTS is too large, we got MinCommitTS: %d, and AttemptedCommitTS: %d",
					rejected.MinCommitTs, rejected.AttemptedCommitTs)
				return errors.Trace(err)
			}

			// Update commit ts and retry.
			commitTS, err := c.store.getTimestampWithRetry(bo)
			if err != nil {
				logutil.Logger(bo.ctx).Warn("2PC get commitTS failed",
					zap.Error(err),
					zap.Uint64("txnStartTS", c.startTS))
				return errors.Trace(err)
			}

			c.mu.Lock()
			c.commitTS = commitTS
			c.mu.Unlock()
			return c.commitMutations(bo, batch.mutations)
		}

		c.mu.RLock()
		defer c.mu.RUnlock()
		err = extractKeyErr(keyErr)
		if c.mu.committed {
			// No secondary key could be rolled back after it's primary key is committed.
			// There must be a serious bug somewhere.
			hexBatchKeys := func(keys [][]byte) []string {
				var res []string
				for _, k := range keys {
					res = append(res, hex.EncodeToString(k))
				}
				return res
			}
			logutil.Logger(bo.ctx).Error("2PC failed commit key after primary key committed",
				zap.Error(err),
				zap.Stringer("primaryKey", kv.Key(c.primaryKey)),
				zap.Uint64("txnStartTS", c.startTS),
				zap.Uint64("commitTS", c.commitTS),
				zap.Uint64("forUpdateTS", c.forUpdateTS),
				zap.Strings("keys", hexBatchKeys(batch.mutations.keys)))
			return errors.Trace(err)
		}
		// The transaction maybe rolled back by concurrent transactions.
		logutil.Logger(bo.ctx).Debug("2PC failed commit primary key",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Group that contains primary key is always the first.
	// We mark transaction's status committed when we receive the first success response.
	c.mu.committed = true
	return nil
}

func (actionCleanup) handleSingleBatch(c *twoPhaseCommitter, bo *Backoffer, batch batchMutations) error {
	req := tikvrpc.NewRequest(tikvrpc.CmdBatchRollback, &pb.BatchRollbackRequest{
		Keys:         batch.mutations.keys,
		StartVersion: c.startTS,
	}, pb.Context{Priority: c.priority, SyncLog: c.syncLog})
	resp, err := c.store.SendReq(bo, req, batch.region, readTimeoutShort)
	if err != nil {
		return errors.Trace(err)
	}
	regionErr, err := resp.GetRegionError()
	if err != nil {
		return errors.Trace(err)
	}
	if regionErr != nil {
		err = bo.Backoff(BoRegionMiss, errors.New(regionErr.String()))
		if err != nil {
			return errors.Trace(err)
		}
		err = c.cleanupMutations(bo, batch.mutations)
		return errors.Trace(err)
	}
	if keyErr := resp.Resp.(*pb.BatchRollbackResponse).GetError(); keyErr != nil {
		err = errors.Errorf("conn %d 2PC cleanup failed: %s", c.connID, keyErr)
		logutil.BgLogger().Debug("2PC failed cleanup key",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
		return errors.Trace(err)
	}
	return nil
}

func (c *twoPhaseCommitter) prewriteMutations(bo *Backoffer, mutations CommitterMutations) error {
	if span := opentracing.SpanFromContext(bo.ctx); span != nil && span.Tracer() != nil {
		span1 := span.Tracer().StartSpan("twoPhaseCommitter.prewriteMutations", opentracing.ChildOf(span.Context()))
		defer span1.Finish()
		bo.ctx = opentracing.ContextWithSpan(bo.ctx, span1)
	}

	return c.doActionOnMutations(bo, actionPrewrite{}, mutations)
}

func (c *twoPhaseCommitter) commitMutations(bo *Backoffer, mutations CommitterMutations) error {
	if span := opentracing.SpanFromContext(bo.ctx); span != nil && span.Tracer() != nil {
		span1 := span.Tracer().StartSpan("twoPhaseCommitter.commitMutations", opentracing.ChildOf(span.Context()))
		defer span1.Finish()
		bo.ctx = opentracing.ContextWithSpan(bo.ctx, span1)
	}

	return c.doActionOnMutations(bo, actionCommit{}, mutations)
}

func (c *twoPhaseCommitter) cleanupMutations(bo *Backoffer, mutations CommitterMutations) error {
	return c.doActionOnMutations(bo, actionCleanup{}, mutations)
}

func (c *twoPhaseCommitter) pessimisticLockMutations(bo *Backoffer, lockCtx *kv.LockCtx, mutations CommitterMutations) error {
	if c.connID > 0 {
		failpoint.Inject("beforePessimisticLock", func(val failpoint.Value) {
			// Pass multiple instructions in one string, delimited by commas, to trigger multiple behaviors, like
			// `return("delay,fail")`. Then they will be executed sequentially at once.
			if v, ok := val.(string); ok {
				for _, action := range strings.Split(v, ",") {
					if action == "delay" {
						duration := time.Duration(rand.Int63n(int64(time.Second) * 5))
						logutil.Logger(bo.ctx).Info("[failpoint] injected delay at pessimistic lock",
							zap.Uint64("txnStartTS", c.startTS), zap.Duration("duration", duration))
						time.Sleep(duration)
					} else if action == "fail" {
						logutil.Logger(bo.ctx).Info("[failpoint] injected failure at pessimistic lock",
							zap.Uint64("txnStartTS", c.startTS))
						failpoint.Return(errors.New("injected failure at pessimistic lock"))
					}
				}
			}
		})
	}
	return c.doActionOnMutations(bo, actionPessimisticLock{lockCtx}, mutations)
}

func (c *twoPhaseCommitter) pessimisticRollbackMutations(bo *Backoffer, mutations CommitterMutations) error {
	return c.doActionOnMutations(bo, actionPessimisticRollback{}, mutations)
}

// execute executes the two-phase commit protocol.
func (c *twoPhaseCommitter) execute(ctx context.Context) (err error) {
	var binlogSkipped bool
	defer func() {
		// Always clean up all written keys if the txn does not commit.
		c.mu.RLock()
		committed := c.mu.committed
		undetermined := c.mu.undeterminedErr != nil
		c.mu.RUnlock()
		if !committed && !undetermined {
			c.cleanWg.Add(1)
			go func() {
				failpoint.Inject("commitFailedSkipCleanup", func() {
					logutil.Logger(ctx).Info("[failpoint] injected skip cleanup secondaries on failure",
						zap.Uint64("txnStartTS", c.startTS))
					c.cleanWg.Done()
					failpoint.Return()
				})

				cleanupKeysCtx := context.WithValue(context.Background(), txnStartKey, ctx.Value(txnStartKey))
				err := c.cleanupMutations(NewBackofferWithVars(cleanupKeysCtx, cleanupMaxBackoff, c.txn.vars), c.mutations)
				if err != nil {
					tikvSecondaryLockCleanupFailureCounterRollback.Inc()
					logutil.Logger(ctx).Info("2PC cleanup failed",
						zap.Error(err),
						zap.Uint64("txnStartTS", c.startTS))
				} else {
					logutil.Logger(ctx).Info("2PC clean up done",
						zap.Uint64("txnStartTS", c.startTS))
				}
				c.cleanWg.Done()
			}()
		}
		c.txn.commitTS = c.commitTS
		if binlogSkipped {
			binloginfo.RemoveOneSkippedCommitter()
		} else {
			if err != nil {
				c.writeFinishBinlog(ctx, binlog.BinlogType_Rollback, 0)
			} else {
				c.writeFinishBinlog(ctx, binlog.BinlogType_Commit, int64(c.commitTS))
			}
		}
	}()

	binlogChan := c.prewriteBinlog(ctx)
	prewriteBo := NewBackofferWithVars(ctx, PrewriteMaxBackoff, c.txn.vars)
	start := time.Now()
	err = c.prewriteMutations(prewriteBo, c.mutations)
	commitDetail := c.getDetail()
	commitDetail.PrewriteTime = time.Since(start)
	if prewriteBo.totalSleep > 0 {
		atomic.AddInt64(&commitDetail.CommitBackoffTime, int64(prewriteBo.totalSleep)*int64(time.Millisecond))
		commitDetail.Mu.Lock()
		commitDetail.Mu.BackoffTypes = append(commitDetail.Mu.BackoffTypes, prewriteBo.types...)
		commitDetail.Mu.Unlock()
	}
	if binlogChan != nil {
		startWaitBinlog := time.Now()
		binlogWriteResult := <-binlogChan
		commitDetail.WaitPrewriteBinlogTime = time.Since(startWaitBinlog)
		if binlogWriteResult != nil {
			binlogSkipped = binlogWriteResult.Skipped()
			binlogErr := binlogWriteResult.GetError()
			if binlogErr != nil {
				return binlogErr
			}
		}
	}
	if err != nil {
		logutil.Logger(ctx).Debug("2PC failed on prewrite",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
		return errors.Trace(err)
	}

	// strip check_not_exists keys that no need to commit.
	c.stripNoNeedCommitKeys()

	start = time.Now()
	logutil.Event(ctx, "start get commit ts")
	commitTS, err := c.store.getTimestampWithRetry(NewBackofferWithVars(ctx, tsoMaxBackoff, c.txn.vars))
	if err != nil {
		logutil.Logger(ctx).Warn("2PC get commitTS failed",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
		return errors.Trace(err)
	}
	commitDetail.GetCommitTsTime = time.Since(start)
	logutil.Event(ctx, "finish get commit ts")
	logutil.SetTag(ctx, "commitTs", commitTS)

	tryAmend := c.isPessimistic && c.connID > 0
	if !tryAmend {
		_, _, err = c.checkSchemaValid(ctx, commitTS, c.txn.txnInfoSchema, false)
		if err != nil {
			return errors.Trace(err)
		}
	} else {
		relatedSchemaChange, memAmended, err := c.checkSchemaValid(ctx, commitTS, c.txn.txnInfoSchema, true)
		if err != nil {
			return errors.Trace(err)
		}
		if memAmended {
			// Get new commitTS and check schema valid again.
			newCommitTS, err := c.getCommitTS(ctx, commitDetail)
			if err != nil {
				return errors.Trace(err)
			}
			// If schema check failed between commitTS and newCommitTs, report schema change error.
			_, _, err = c.checkSchemaValid(ctx, newCommitTS, relatedSchemaChange.LatestInfoSchema, false)
			if err != nil {
				logutil.Logger(ctx).Info("schema check after amend failed, it means the schema version changed again",
					zap.Uint64("startTS", c.startTS),
					zap.Uint64("amendTS", c.commitTS),
					zap.Int64("amendedSchemaVersion", relatedSchemaChange.LatestInfoSchema.SchemaMetaVersion()),
					zap.Uint64("newCommitTS", newCommitTS))
				return errors.Trace(err)
			}
			commitTS = newCommitTS
		}
	}
	c.commitTS = commitTS

	if c.store.oracle.IsExpired(c.startTS, kv.MaxTxnTimeUse) {
		err = errors.Errorf("conn %d txn takes too much time, txnStartTS: %d, comm: %d",
			c.connID, c.startTS, c.commitTS)
		return err
	}

	if c.connID > 0 {
		failpoint.Inject("beforeCommit", func(val failpoint.Value) {
			// Pass multiple instructions in one string, delimited by commas, to trigger multiple behaviors, like
			// `return("delay,fail")`. Then they will be executed sequentially at once.
			if v, ok := val.(string); ok {
				for _, action := range strings.Split(v, ",") {
					// Async commit transactions cannot return error here, since it's already successful.
					if action == "fail" {
						logutil.Logger(ctx).Info("[failpoint] injected failure before commit", zap.Uint64("txnStartTS", c.startTS))
						failpoint.Return(errors.New("injected failure before commit"))
					} else if action == "delay" {
						duration := time.Duration(rand.Int63n(int64(time.Second) * 5))
						logutil.Logger(ctx).Info("[failpoint] injected delay before commit",
							zap.Uint64("txnStartTS", c.startTS), zap.Duration("duration", duration))
						time.Sleep(duration)
					}
				}
			}
		})
	}

	start = time.Now()
	commitBo := NewBackofferWithVars(ctx, CommitMaxBackoff, c.txn.vars)
	err = c.commitMutations(commitBo, c.mutations)
	commitDetail.CommitTime = time.Since(start)
	if commitBo.totalSleep > 0 {
		atomic.AddInt64(&commitDetail.CommitBackoffTime, int64(commitBo.totalSleep)*int64(time.Millisecond))
		commitDetail.Mu.Lock()
		commitDetail.Mu.BackoffTypes = append(commitDetail.Mu.BackoffTypes, commitBo.types...)
		commitDetail.Mu.Unlock()
	}
	if err != nil {
		if undeterminedErr := c.getUndeterminedErr(); undeterminedErr != nil {
			logutil.Logger(ctx).Error("2PC commit result undetermined",
				zap.Error(err),
				zap.NamedError("rpcErr", undeterminedErr),
				zap.Uint64("txnStartTS", c.startTS))
			err = errors.Trace(terror.ErrResultUndetermined)
		}
		if !c.mu.committed {
			logutil.Logger(ctx).Debug("2PC failed on commit",
				zap.Error(err),
				zap.Uint64("txnStartTS", c.startTS))
			return errors.Trace(err)
		}
		logutil.Logger(ctx).Debug("got some exceptions, but 2PC was still successful",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
	}
	return nil
}

func (c *twoPhaseCommitter) stripNoNeedCommitKeys() {
	if len(c.noNeedCommitKeys) == 0 {
		return
	}
	m := &c.mutations
	var newIdx int
	for oldIdx := range m.keys {
		key := m.keys[oldIdx]
		if _, ck := c.noNeedCommitKeys[string(key)]; ck {
			continue
		}
		m.keys[newIdx] = key
		if m.ops != nil {
			m.ops[newIdx] = m.ops[oldIdx]
		}
		if m.values != nil {
			m.values[newIdx] = m.values[oldIdx]
		}
		if m.isPessimisticLock != nil {
			m.isPessimisticLock[newIdx] = m.isPessimisticLock[oldIdx]
		}
		newIdx++
	}
	c.mutations = m.subRange(0, newIdx)
}

// SchemaVer is the infoSchema which will return the schema version.
type SchemaVer interface {
	// SchemaMetaVersion returns the meta schema version.
	SchemaMetaVersion() int64
}

type schemaLeaseChecker interface {
	// CheckBySchemaVer checks if the schema has changed for the transaction related tables between the startSchemaVer
	// and the schema version at txnTS, all the related schema changes will be returned.
	CheckBySchemaVer(txnTS uint64, startSchemaVer SchemaVer) (*RelatedSchemaChange, error)
}

// RelatedSchemaChange contains information about schema diff between two schema versions.
type RelatedSchemaChange struct {
	PhyTblIDS        []int64
	ActionTypes      []uint64
	LatestInfoSchema SchemaVer
	Amendable        bool
}

func (c *twoPhaseCommitter) tryAmendTxn(ctx context.Context, startInfoSchema SchemaVer, change *RelatedSchemaChange) (bool, error) {
	addMutations, err := c.txn.schemaAmender.AmendTxn(ctx, startInfoSchema, change, c.mutations)
	if err != nil {
		return false, err
	}
	// Prewrite new mutations.
	if addMutations != nil && len(addMutations.keys) > 0 {
		var keysNeedToLock CommitterMutations
		for i := 0; i < addMutations.len(); i++ {
			if addMutations.isPessimisticLock[i] {
				keysNeedToLock.Push(addMutations.ops[i], addMutations.keys[i], addMutations.values[i], addMutations.isPessimisticLock[i])
			}
		}
		// For unique index amend, we need to pessimistic lock the generated new index keys first.
		// Set doingAmend to true to force the pessimistic lock do the exist check for these keys.
		c.doingAmend = true
		defer func() { c.doingAmend = false }()
		if keysNeedToLock.len() > 0 {
			lCtx := &kv.LockCtx{
				Killed:        c.lockCtx.Killed,
				ForUpdateTS:   c.forUpdateTS,
				LockWaitTime:  c.lockCtx.LockWaitTime,
				WaitStartTime: time.Now(),
			}
			tryTimes := uint(0)
			retryLimit := config.GetGlobalConfig().PessimisticTxn.MaxRetryCount
			for tryTimes < retryLimit {
				pessimisticLockBo := NewBackofferWithVars(ctx, pessimisticLockMaxBackoff, c.txn.vars)
				err = c.pessimisticLockMutations(pessimisticLockBo, lCtx, keysNeedToLock)
				if err != nil {
					// KeysNeedToLock won't change, so don't async rollback pessimistic locks here for write conflict.
					if terror.ErrorEqual(kv.ErrWriteConflict, err) {
						newForUpdateTSVer, err := c.store.CurrentVersion()
						if err != nil {
							return false, errors.Trace(err)
						}
						lCtx.ForUpdateTS = newForUpdateTSVer.Ver
						c.forUpdateTS = newForUpdateTSVer.Ver
						logutil.Logger(ctx).Info("amend pessimistic lock pessimistic retry lock",
							zap.Uint("tryTimes", tryTimes), zap.Uint64("startTS", c.startTS),
							zap.Uint64("newForUpdateTS", c.forUpdateTS))
						tryTimes++
						continue
					}
					logutil.Logger(ctx).Warn("amend pessimistic lock has failed", zap.Error(err), zap.Uint64("txnStartTS", c.startTS))
					return false, err
				}
				logutil.Logger(ctx).Info("amend pessimistic lock finished", zap.Uint64("startTS", c.startTS),
					zap.Uint64("forUpdateTS", c.forUpdateTS), zap.Int("keys", keysNeedToLock.len()))
				break
			}
			if err != nil {
				logutil.Logger(ctx).Warn("amend pessimistic lock failed after retry",
					zap.Uint("tryTimes", tryTimes), zap.Uint64("startTS", c.startTS))
				return false, err
			}
		}
		prewriteBo := NewBackofferWithVars(ctx, PrewriteMaxBackoff, c.txn.vars)
		err = c.prewriteMutations(prewriteBo, *addMutations)
		if err != nil {
			logutil.Logger(ctx).Warn("amend prewrite has failed", zap.Error(err), zap.Uint64("txnStartTS", c.startTS))
			return false, err
		}
		// Commit the amended secondary keys in the commit phase.
		c.mutations.MergeMutations(*addMutations)
		logutil.Logger(ctx).Info("amend prewrite finished", zap.Uint64("txnStartTS", c.startTS))
		return true, nil
	}
	return false, nil
}

func (c *twoPhaseCommitter) getCommitTS(ctx context.Context, commitDetail *execdetails.CommitDetails) (uint64, error) {
	start := time.Now()
	logutil.Event(ctx, "start get commit ts")
	commitTS, err := c.store.getTimestampWithRetry(NewBackofferWithVars(ctx, tsoMaxBackoff, c.txn.vars))
	if err != nil {
		logutil.Logger(ctx).Warn("2PC get commitTS failed",
			zap.Error(err),
			zap.Uint64("txnStartTS", c.startTS))
		return 0, errors.Trace(err)
	}
	commitDetail.GetCommitTsTime = time.Since(start)
	logutil.Event(ctx, "finish get commit ts")
	logutil.SetTag(ctx, "commitTS", commitTS)

	// Check commitTS.
	if commitTS <= c.startTS {
		err = errors.Errorf("conn %d invalid transaction tso with txnStartTS=%v while txnCommitTS=%v",
			c.connID, c.startTS, commitTS)
		logutil.BgLogger().Error("invalid transaction", zap.Error(err))
		return 0, errors.Trace(err)
	}
	return commitTS, nil
}

// checkSchemaValid checks if the schema has changed, if tryAmend is set to true, committer will try to amend
// this transaction using the related schema changes.
func (c *twoPhaseCommitter) checkSchemaValid(ctx context.Context, checkTS uint64, startInfoSchema SchemaVer,
	tryAmend bool) (*RelatedSchemaChange, bool, error) {
	checker, ok := c.txn.us.GetOption(kv.SchemaChecker).(schemaLeaseChecker)
	if !ok {
		if c.connID > 0 {
			logutil.Logger(ctx).Warn("schemaLeaseChecker is not set for this transaction, schema check skipped",
				zap.Uint64("connID", c.connID), zap.Uint64("startTS", c.startTS), zap.Uint64("commitTS", checkTS))
		}
		return nil, false, nil
	}
	relatedChanges, err := checker.CheckBySchemaVer(checkTS, startInfoSchema)
	if err != nil {
		if tryAmend && relatedChanges != nil && relatedChanges.Amendable && c.txn.schemaAmender != nil {
			memAmended, amendErr := c.tryAmendTxn(ctx, startInfoSchema, relatedChanges)
			if amendErr != nil {
				logutil.BgLogger().Info("txn amend has failed", zap.Uint64("connID", c.connID),
					zap.Uint64("startTS", c.startTS), zap.Error(amendErr))
				return nil, false, err
			}
			logutil.Logger(ctx).Info("amend txn successfully for pessimistic commit",
				zap.Uint64("connID", c.connID), zap.Uint64("txn startTS", c.startTS), zap.Bool("memAmended", memAmended),
				zap.Uint64("checkTS", checkTS), zap.Int64("startInfoSchemaVer", startInfoSchema.SchemaMetaVersion()),
				zap.Int64s("table ids", relatedChanges.PhyTblIDS), zap.Uint64s("action types", relatedChanges.ActionTypes))
			return relatedChanges, memAmended, nil
		}
		return nil, false, errors.Trace(err)
	}
	return nil, false, nil
}

func (c *twoPhaseCommitter) prewriteBinlog(ctx context.Context) chan *binloginfo.WriteResult {
	if !c.shouldWriteBinlog() {
		return nil
	}
	ch := make(chan *binloginfo.WriteResult, 1)
	go func() {
		logutil.Eventf(ctx, "start prewrite binlog")
		binInfo := c.txn.us.GetOption(kv.BinlogInfo).(*binloginfo.BinlogInfo)
		bin := binInfo.Data
		bin.StartTs = int64(c.startTS)
		if bin.Tp == binlog.BinlogType_Prewrite {
			bin.PrewriteKey = c.primary()
		}
		wr := binInfo.WriteBinlog(c.store.clusterID)
		if wr.Skipped() {
			binInfo.Data.PrewriteValue = nil
			binloginfo.AddOneSkippedCommitter()
		}
		logutil.Eventf(ctx, "finish prewrite binlog")
		ch <- wr
	}()
	return ch
}

func (c *twoPhaseCommitter) writeFinishBinlog(ctx context.Context, tp binlog.BinlogType, commitTS int64) {
	if !c.shouldWriteBinlog() {
		return
	}
	binInfo := c.txn.us.GetOption(kv.BinlogInfo).(*binloginfo.BinlogInfo)
	binInfo.Data.Tp = tp
	binInfo.Data.CommitTs = commitTS
	binInfo.Data.PrewriteValue = nil

	wg := sync.WaitGroup{}
	mock := false
	failpoint.Inject("mockSyncBinlogCommit", func(val failpoint.Value) {
		if val.(bool) {
			wg.Add(1)
			mock = true
		}
	})
	go func() {
		logutil.Eventf(ctx, "start write finish binlog")
		binlogWriteResult := binInfo.WriteBinlog(c.store.clusterID)
		err := binlogWriteResult.GetError()
		if err != nil {
			logutil.BgLogger().Error("failed to write binlog",
				zap.Error(err))
		}
		logutil.Eventf(ctx, "finish write finish binlog")
		if mock {
			wg.Done()
		}
	}()
	if mock {
		wg.Wait()
	}
}

func (c *twoPhaseCommitter) shouldWriteBinlog() bool {
	return c.txn.us.GetOption(kv.BinlogInfo) != nil
}

// TiKV recommends each RPC packet should be less than ~1MB. We keep each packet's
// Key+Value size below 16KB.
const txnCommitBatchSize = 16 * 1024

type batchMutations struct {
	region    RegionVerID
	mutations CommitterMutations
	isPrimary bool
}

// appendBatchMutationsBySize appends mutations to b. It may split the keys to make
// sure each batch's size does not exceed the limit.
func (c *twoPhaseCommitter) appendBatchMutationsBySize(b []batchMutations, region RegionVerID, mutations CommitterMutations, sizeFn func(k, v []byte) int, limit int, primaryIdx *int) []batchMutations {
	failpoint.Inject("twoPCRequestBatchSizeLimit", func() {
		limit = 1
	})

	var start, end int
	for start = 0; start < mutations.len(); start = end {
		var size int
		for end = start; end < mutations.len() && size < limit; end++ {
			var k, v []byte
			k = mutations.keys[end]
			if end < len(mutations.values) {
				v = mutations.values[end]
			}
			size += sizeFn(k, v)
			if *primaryIdx < 0 && bytes.Equal(k, c.primary()) {
				*primaryIdx = len(b)
			}
		}
		b = append(b, batchMutations{
			region:    region,
			mutations: mutations.subRange(start, end),
		})
	}
	return b
}

// newBatchExecutor create processor to handle concurrent batch works(prewrite/commit etc)
func newBatchExecutor(rateLimit int, committer *twoPhaseCommitter,
	action twoPhaseCommitAction, backoffer *Backoffer) *batchExecutor {
	return &batchExecutor{rateLimit, nil, committer,
		action, backoffer, time.Duration(1 * time.Millisecond)}
}

// initUtils do initialize batchExecutor related policies like rateLimit util
func (batchExe *batchExecutor) initUtils() error {
	// init rateLimiter by injected rate limit number
	batchExe.rateLimiter = newRateLimit(batchExe.rateLim)
	return nil
}

// startWork concurrently do the work for each batch considering rate limit
func (batchExe *batchExecutor) startWorker(exitCh chan struct{}, ch chan error, batches []batchMutations) {
	for idx, batch1 := range batches {
		waitStart := time.Now()
		if exit := batchExe.rateLimiter.getToken(exitCh); !exit {
			batchExe.tokenWaitDuration += time.Since(waitStart)
			batch := batch1
			go func() {
				defer batchExe.rateLimiter.putToken()
				var singleBatchBackoffer *Backoffer
				if _, ok := batchExe.action.(actionCommit); ok {
					// Because the secondary batches of the commit actions are implemented to be
					// committed asynchronously in background goroutines, we should not
					// fork a child context and call cancel() while the foreground goroutine exits.
					// Otherwise the background goroutines will be canceled execeptionally.
					// Here we makes a new clone of the original backoffer for this goroutine
					// exclusively to avoid the data race when using the same backoffer
					// in concurrent goroutines.
					singleBatchBackoffer = batchExe.backoffer.Clone()
				} else {
					var singleBatchCancel context.CancelFunc
					singleBatchBackoffer, singleBatchCancel = batchExe.backoffer.Fork()
					defer singleBatchCancel()
				}
				beforeSleep := singleBatchBackoffer.totalSleep
				ch <- batchExe.action.handleSingleBatch(batchExe.committer, singleBatchBackoffer, batch)
				commitDetail := batchExe.committer.getDetail()
				if commitDetail != nil { // lock operations of pessimistic-txn will let commitDetail be nil
					if delta := singleBatchBackoffer.totalSleep - beforeSleep; delta > 0 {
						atomic.AddInt64(&commitDetail.CommitBackoffTime, int64(singleBatchBackoffer.totalSleep-beforeSleep)*int64(time.Millisecond))
						commitDetail.Mu.Lock()
						commitDetail.Mu.BackoffTypes = append(commitDetail.Mu.BackoffTypes, singleBatchBackoffer.types...)
						commitDetail.Mu.Unlock()
					}
				}
			}()
		} else {
			logutil.Logger(batchExe.backoffer.ctx).Info("break startWorker",
				zap.Stringer("action", batchExe.action), zap.Int("batch size", len(batches)),
				zap.Int("index", idx))
			break
		}
	}
}

// process will start worker routine and collect results
func (batchExe *batchExecutor) process(batches []batchMutations) error {
	var err error
	err = batchExe.initUtils()
	if err != nil {
		logutil.Logger(batchExe.backoffer.ctx).Error("batchExecutor initUtils failed", zap.Error(err))
		return err
	}

	// For prewrite, stop sending other requests after receiving first error.
	var cancel context.CancelFunc
	if _, ok := batchExe.action.(actionPrewrite); ok {
		batchExe.backoffer, cancel = batchExe.backoffer.Fork()
		defer cancel()
	}
	// concurrently do the work for each batch.
	ch := make(chan error, len(batches))
	exitCh := make(chan struct{})
	go batchExe.startWorker(exitCh, ch, batches)
	// check results
	for i := 0; i < len(batches); i++ {
		if e := <-ch; e != nil {
			logutil.Logger(batchExe.backoffer.ctx).Debug("2PC doActionOnBatch failed",
				zap.Uint64("conn", batchExe.committer.connID),
				zap.Stringer("action type", batchExe.action),
				zap.Error(e),
				zap.Uint64("txnStartTS", batchExe.committer.startTS))
			// Cancel other requests and return the first error.
			if cancel != nil {
				logutil.Logger(batchExe.backoffer.ctx).Debug("2PC doActionOnBatch to cancel other actions",
					zap.Uint64("conn", batchExe.committer.connID),
					zap.Stringer("action type", batchExe.action),
					zap.Uint64("txnStartTS", batchExe.committer.startTS))
				cancel()
			}
			if err == nil {
				err = e
			}
		}
	}
	close(exitCh)
	metrics.TiKVTokenWaitDuration.Observe(batchExe.tokenWaitDuration.Seconds())
	return err
}
