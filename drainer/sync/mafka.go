package sync

//#cgo CFLAGS: -I /usr/local/include
//#cgo LDFLAGS: -L ../common  -Wl,-rpath=/usr/local/lib -lcommon
//
//#include "libcommon.h"
import "C"

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/pingcap/tidb-binlog/drainer/loopbacksync"
	"github.com/pingcap/tidb-binlog/drainer/relay"
	"github.com/pingcap/tidb-binlog/drainer/translator"
	"github.com/pingcap/tidb/store/tikv/oracle"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

type MafkaSyncer struct {
	toBeAckCommitTSMu      sync.Mutex
	toBeAckCommitTS *MapList
	shutdown chan struct{}
	maxWaitThreshold int64
	safemode   int
	tableInfos *TableInformations
	*baseSyncer
}

func NewMafkaSyncer (
	cfg *DBConfig,
	cfgFile string,
	tableInfoGetter translator.TableInfoGetter,
	worker int,
	batchSize int,
	queryHistogramVec *prometheus.HistogramVec,
	sqlMode *string,
	destDBType string,
	relayer relay.Relayer,
	info *loopbacksync.LoopBackSync) (dsyncer Syncer, err error) {
	if cfgFile == "" {
		return nil, errors.New("config file name is empty")
	}

	ret := C.InitProducerOnce(C.CString(cfgFile))
	if len(C.GoString(ret)) > 0 {
		return nil, errors.New("init producer error: " + C.GoString(ret))
	}

	time.Sleep(5 * time.Second)

	executor := &MafkaSyncer{}
	executor.shutdown = make(chan struct{})
	executor.toBeAckCommitTS = NewMapList()
	executor.baseSyncer = newBaseSyncer(tableInfoGetter)
	executor.maxWaitThreshold = int64(C.GetWaitThreshold())
	executor.safemode = int(C.GetSafeMode())

	log.Info("init syncer args", zap.Int64("maxWaitThreshold", executor.maxWaitThreshold), zap.Int("safemode", executor.safemode))

	is, err := NewTableInformations(cfg.Checkpoint.User, cfg.Checkpoint.Password, cfg.Host, cfg.Port)
	if err != nil {
		return nil, err
	}
	log.Info("checkpoint", zap.String("user", cfg.Checkpoint.User), zap.String("pwd", cfg.Checkpoint.Password),
		zap.String("host", cfg.Checkpoint.Host), zap.Int("port", cfg.Port))
	executor.tableInfos = is

	log.Info("New MafkaSyncer success")
	go executor.Run()

	return executor, nil
}

func (ms *MafkaSyncer) Sync(item *Item) error {
	txn, err := translator.TiBinlogToTxn(ms.tableInfoGetter, item.Schema, item.Table, item.Binlog, item.PrewriteValue, item.ShouldSkip)
	if err != nil {
		return errors.Trace(err)
	}

	tso := item.Binlog.GetCommitTs()
	cts := oracle.ExtractPhysical(uint64(tso))
	ats := time.Now().UnixNano()/1000000
	log.Info("txn", zap.String("txn info", fmt.Sprintf("%v", txn)))

	if txn.DDL != nil {
		log.Info("Mafka->DDL", zap.String("sql", fmt.Sprintf("%v", txn.DDL.SQL)), zap.Int64("diff(ms)", ats - cts),
			zap.Int64("tso", cts), zap.Int64("sequence", int64(0)))
		/*
		sqls := strings.Split(txn.DDL.SQL, ";")
		for seq, sql := range sqls {
			log.Info("Mafka->DDL", zap.String("sql", fmt.Sprintf("%v", sql)), zap.Int64("diff(ms)", ats - cts),
				zap.Int64("tso", cts), zap.Int64("sequence", int64(seq)))
			//C.AsyncMessage(C.CString(txn.DDL.Database), C.CString(txn.DDL.Table), C.CString(string(sql)), C.long(cts), C.long(ats), C.long(tso), C.long(seq))
		}
		*/
	} else {
		for seq, dml := range txn.DMLs {
			i, e := ms.tableInfos.GetFromInfos(dml.Database, dml.Table)
			if e != nil {
				return err
			}
			dml.SetTableInfo(i)
			normal, args := dml.SqlWithSafeMode(ms.safemode)
			sql, err := GenSQL(normal, args, true, time.Local)
			if err != nil {
				log.Warn("genSQL error", zap.Error(err))
				return err
			}
			log.Info("Mafka->DML", zap.String("sql", fmt.Sprintf("%v", sql)), zap.Int64("latency", ats - cts),
				zap.Int64("sequence", int64(seq)))
			//C.AsyncMessage(C.CString(dml.Database), C.CString(dml.Table), C.CString(sql), C.long(cts), C.long(ats), C.long(tso), C.long(seq))
		}
	}

	ms.success <- item
	log.Info("##### DDL return direct")
	return nil

	ms.toBeAckCommitTSMu.Lock()
	ms.toBeAckCommitTS.Push(item)
	ms.toBeAckCommitTSMu.Unlock()

	return nil
}

func (ms *MafkaSyncer) Close() error {
	if ms.shutdown != nil {
		close(ms.shutdown)
		ms.shutdown = nil
	}
	return nil
}

func (ms *MafkaSyncer) SetSafeMode(mode bool) bool {
	return false
}

func (ms *MafkaSyncer) Run () {
	var wg sync.WaitGroup
	log.Info("MafkaSyncer Running now")
	// handle successes from producer
	wg.Add(1)
	go func() {
		defer wg.Done()

		checkTick := time.NewTicker(200 * time.Millisecond)
		defer checkTick.Stop()
		for {
			select {
			case <-checkTick.C:
				ts := int64(C.GetLatestApplyTime())
				if ts > 0 {
					ms.toBeAckCommitTSMu.Lock()
					var next *list.Element
					for elem := ms.toBeAckCommitTS.GetDataList().Front(); elem != nil; elem = next {
						if elem.Value.(Keyer).GetKey() <= ts {
							next = elem.Next()
							ms.success <- elem.Value.(*Item)
							ms.toBeAckCommitTS.Remove(elem.Value.(Keyer))
						} else {
							break
						}
					}
					ms.toBeAckCommitTSMu.Unlock()
				}

				ms.toBeAckCommitTSMu.Lock()
				tss := int64(C.GetLatestSuccessTime())
				cur := time.Now().UnixNano()
				if ms.toBeAckCommitTS.Size() > 0 && cur != 0 && (cur - tss) > ms.maxWaitThreshold * 1000000 {
					err := errors.New(fmt.Sprintf("fail to push msg to mafka after %v, check if kafka is up and working", ms.maxWaitThreshold))
					ms.setErr(err)
					log.Warn("fail to push msg to mafka, MafkaSyncer exit")
					close(ms.shutdown)
				}
				ms.toBeAckCommitTSMu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ms.shutdown:
			wg.Wait()
			log.Info("MafkaSyncer exited")
			C.CloseProducer()
			ms.setErr(nil)
			return
		}
	}
}

func (it *Item) GetKey() int64 {
	return it.Binlog.GetCommitTs()
}

type Message struct {
	database string `json:"database-name"`
	table    string `json:"table-name"`
	Sql      string `json:"sql"`
	Cts      int64  `json:"committed-timestamp"`
	Ats      int64  `json:"applied-timestamp"`
}

func NewMessage(db, tb, sql string, cts, ats int64) *Message {
	return &Message{
		database: db,
		table:    tb,
		Sql:      sql,
		Cts:      oracle.ExtractPhysical(uint64(cts)),
		Ats:      ats,
	}
}