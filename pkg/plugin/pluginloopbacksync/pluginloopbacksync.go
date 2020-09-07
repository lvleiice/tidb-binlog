package main

import (
    "database/sql"
    gosql "database/sql"
    "fmt"
    "strings"
    "sync/atomic"

    "github.com/pingcap/errors"
    "github.com/pingcap/log"
    "github.com/pingcap/tidb-binlog/drainer/loopbacksync"
    "github.com/pingcap/tidb-binlog/drainer/sync"
    "github.com/pingcap/tidb-binlog/pkg/loader"
    "go.uber.org/zap"
)

// Plugin for loopbacksync
type Plugin struct{}

const (
    // ID field in mark table
    ID = "id"
    // Val field in mark table
    Val = "val"
)

func addIndex(info *loopbacksync.LoopBackSync) int64 {
    return atomic.AddInt64(&info.Index, 1) % ((int64)(info.RecordID))
}

func createMarkTable(db *sql.DB, markTableName string) error {
    sql := fmt.Sprintf(
        "CREATE TABLE If Not Exists %s (" +
            "%s bigint not null," +
            "%s bigint not null DEFAULT 0, " +
            "PRIMARY KEY (%s));",
        markTableName, ID,Val,ID)
    _, err := db.Exec(sql)
    if err != nil {
        return errors.Annotate(err, "failed to create mark table")
    }

    return nil
}

func initMarkTableData(db *sql.DB, markTableName string, rowNum int) error {
    var builder strings.Builder
    holder := "(?,?)"
    columns := fmt.Sprintf("(%s,%s) ", ID, Val)
    builder.WriteString("REPLACE INTO " + markTableName + columns + " VALUES ")
    for i := 0; i < rowNum; i++ {
        if i > 0 {
            builder.WriteByte(',')
        }
        builder.WriteString(holder)
    }

    var args []interface{}
    for id := 0; id < rowNum; id++ {
        args = append(args, id, 1 /* value */)
    }

    query := builder.String()
    if _, err := db.Exec(query, args...); err != nil {
        log.Error("Exec fail", zap.String("query", query), zap.Reflect("args", args), zap.Error(err))
        return errors.Trace(err)
    }

    return nil
}

func findLoopBackMark(dmls []*loader.DML, info *loopbacksync.LoopBackSync) (bool, error) {
    for _, dml := range dmls {
        if strings.EqualFold(dml.Table, info.MarkTableName) {
            log.Info("find loopback mark, no need to handle DML transaction")
            log.Info(logFilterTx(dmls))
            return true, nil
        }
    }
    return false, nil
}

func logFilterTx(dmls []*loader.DML) (str string) {
    str = ""
    str += fmt.Sprintf("#### Tx-Start #### event-count: %d ", len(dmls))
    for i, dml := range dmls {
        str += fmt.Sprintf("num: %d dml: %v", i + 1, dml)
    }
    str += "#### Tx-End ####\n"
    return str
}

// LoaderInit create the mark table and init data
func (p Plugin) LoaderInit(db *gosql.DB, info *loopbacksync.LoopBackSync) error{
    err := createMarkTable(db, info.MarkTableName)
    if err != nil{
        return err
    }
    return initMarkTableData(db, info.MarkTableName, info.RecordID)
}

// LoaderDestroy delete the data from the mark table
func (p Plugin) LoaderDestroy(db *gosql.DB, info *loopbacksync.LoopBackSync) error{
    sql := fmt.Sprintf("delete from %s ", info.MarkTableName)
    _, err := db.Exec(sql)

    if err != nil {
        return errors.Annotate(err, "failed t clean mark table data")
    }

    return nil
}

// ExtendTxn insert an updating mark table statement into the transaction.
func (p Plugin) ExtendTxn(tx *loader.Tx, info *loopbacksync.LoopBackSync) error {
    if tx == nil || info == nil{
        if tx == nil {
            log.Error("tx is nil")
        }
        if info == nil {
            log.Error("info is nil")
        }
        return nil
    }
    /* update mark table to avoid loopback sync */
    sql := fmt.Sprintf("update %s set %s=%s+1 where %s=? limit 1;", info.MarkTableName, Val, Val, ID)
    tx.IsAddProtocolTable = true
    rs, err := tx.Exec(sql, addIndex(info))
    if err != nil {
        tx.IsAddProtocolTable = false
        rerr := tx.Rollback()
        if rerr != nil {
            log.Error("fail to rollback", zap.Error(rerr))
        }
        log.Error("fail to update mark", zap.Error(err))
        return err
    }
    if rs != nil {
        affectedrows, err := rs.RowsAffected()
        if err != nil {
            log.Error("get affected rows failed")
            tx.IsAddProtocolTable = false
            rerr := tx.Rollback()
            if rerr != nil {
                log.Error("fail to rollback", zap.Error(rerr))
            }
            return errors.New("get affected rows failed")
        } else {
            if affectedrows == 0 {
                log.Error("affected rows is zero")
                tx.IsAddProtocolTable = false
                rerr := tx.Rollback()
                if rerr != nil {
                    log.Error("fail to rollback", zap.Error(rerr))
                }
                return errors.New("affected rows is zero")
            }
        }
    }
    return nil
}

// FilterTxn filter the transaction from upstream which is no need to handle
func (p Plugin) FilterTxn(txn *loader.Txn, info *loopbacksync.LoopBackSync) (*loader.Txn, error) {
    if txn == nil || info == nil{
        return nil, nil
    }

    /* skip ddl */
    if txn.DDL != nil {
        log.Info("skip DDL by FilterTxn plugin.", zap.String("sql", txn.DDL.SQL))
        return nil, nil
    }

    /* skip if loopback mark exists */
    find,err := findLoopBackMark(txn.DMLs,info)
    if err!= nil{
        log.Error("analyze transaction failed", zap.Error(err))
        return txn, err
    }
    if find{
        return nil, nil
    }

    for _, ip := range info.MigrationIPs {
        if strings.EqualFold(txn.Ip, ip) {
            log.Fatal("Cyclic replication may occur", zap.String("commit-ts", fmt.Sprintf("%v", txn.Metadata.(*sync.Item).Binlog.CommitTs)),
                zap.String("txn ip", ip), zap.Strings("migration ips", info.MigrationIPs), zap.Strings("txn", txn.GetSQL()))
        }
    }

    /* set Database name empty */
    for _, v := range txn.DMLs {
        v.Database = ""
    }

    return txn, nil
}

// NewPlugin is a flag for go plugin
func NewPlugin() interface{}{
    return Plugin{}
}

var _ Plugin
var _ = NewPlugin()
