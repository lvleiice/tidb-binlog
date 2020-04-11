package main

import (
	"errors"

	"github.com/pingcap/tidb-binlog/drainer/loopbacksync"
	"github.com/pingcap/tidb-binlog/drainer/relay"
	"github.com/pingcap/tidb-binlog/drainer/sync"
	"github.com/pingcap/tidb-binlog/drainer/translator"
	"github.com/prometheus/client_golang/prometheus"
)

//DemoSyncer is a syncer demo
type DemoSyncer struct {
	sync.BaseSyncer
}

//Sync should be implemented
func (ds *DemoSyncer) Sync(item *sync.Item) error {
	return nil
}

//Close should be implemented
func (ds *DemoSyncer) Close() error {
	return nil
}

//NewSyncerPlugin return A syncer instance which implemented interface of sync.Syncer
func NewSyncerPlugin(
	cfg *sync.DBConfig,
	tableInfoGetter translator.TableInfoGetter,
	worker int,
	batchSize int,
	queryHistogramVec *prometheus.HistogramVec,
	sqlMode *string,
	destDBType string,
	relayer relay.Relayer,
	info *loopbacksync.LoopBackSync,
	enableDispatch bool,
	enableCausility bool) (dsyncer sync.Syncer, err error) {
	return nil, errors.New("test error")
}

var _ DemoSyncer