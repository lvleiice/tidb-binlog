package loader

import (
	gosql "database/sql"

	"github.com/pingcap/tidb-binlog/drainer/loopbacksync"
)

// ExecutorExtend is the interface for loader plugin
type ExecutorExtend interface {
	ExtendTxn(tx *Tx, info *loopbacksync.LoopBackSync) error
}

// LoaderExtend is the interface for loader plugin
type LoaderExtend interface {
	FilterTxn(tx *Txn, info *loopbacksync.LoopBackSync) (*Txn, error)
}

// Init is the interface for loader plugin
type Init interface {
	LoaderInit(db *gosql.DB, info *loopbacksync.LoopBackSync) error
}

// Destroy is the interface that for loader-plugin
type Destroy interface {
	LoaderDestroy(db *gosql.DB, info *loopbacksync.LoopBackSync) error
}