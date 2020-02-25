package drainer

import (
	"github.com/pingcap/tidb-binlog/drainer/loopbacksync"
	"github.com/pingcap/tidb-binlog/pkg/loader"
)

type LoopBack interface {
	FilterMarkTable(DMLs []*loader.DML, info *loopbacksync.LoopBackSync) (bool, error)
}
