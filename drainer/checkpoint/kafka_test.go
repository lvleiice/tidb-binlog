package checkpoint

import (
	"path/filepath"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/errors"
)

func (t *testCheckPointSuite) TestKafka(c *C) {
	dir := c.MkDir()
	fileName := filepath.Join(dir, "test_kafka")
	cfg := new(Config)
	cfg.CheckPointFile = fileName
	cp, err := newKafka(cfg)
	c.Assert(err, IsNil)
	c.Assert(cp.TS(), Equals, int64(0))

	testTs := int64(1)
	err = cp.Save(testTs)
	c.Assert(err, IsNil)
	ts := cp.TS()
	c.Assert(ts, Equals, testTs)

	// test for safeTs
	cp2, ok := cp.(*KafkaCheckpoint)
	c.Assert(ok, IsTrue)

	var safeTs int64 = 100
	newTs := safeTs + 1
	cp2.meta.SetSafeTS(safeTs)

	go func() {
		time.Sleep(500 * time.Millisecond) // sleep for a while
		cp2.meta.SetSafeTS(newTs)
	}()

	begin := time.Now()
	err = cp.Save(newTs) // block until `newTs` be set
	c.Assert(err, IsNil)
	c.Assert(cp.TS(), Equals, newTs)
	c.Assert(time.Since(begin).Seconds(), Greater, 0.49) // ~ 0.5

	// close the checkpoint
	err = cp.Close()
	c.Assert(err, IsNil)
	c.Assert(errors.Cause(cp.Load()), Equals, ErrCheckPointClosed)
	c.Assert(errors.Cause(cp.Save(0)), Equals, ErrCheckPointClosed)
	c.Assert(cp.Check(0), IsFalse)
	c.Assert(errors.Cause(cp.Close()), Equals, ErrCheckPointClosed)
}