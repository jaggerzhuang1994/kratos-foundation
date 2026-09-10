package log

import (
	"errors"
	"os"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestOutputReleaseRejectsLaterWritesWithoutFileSink(t *testing.T) {
	out, release, err := newOutputLogger(envConfig{
		Std:  outputConfig{Disable: true},
		File: fileConfig{outputConfig: outputConfig{Disable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Log(kratoslog.LevelInfo, "event", "before-release"); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if err := out.Log(kratoslog.LevelInfo, "event", "after-release"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("released output Log = %v, want os.ErrClosed", err)
	}
}

func TestOutputReleaseWaitsForActiveWrite(t *testing.T) {
	out, release, err := newOutputLogger(envConfig{
		Std:  outputConfig{Disable: true},
		File: fileConfig{outputConfig: outputConfig{Disable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	finishWrite := make(chan struct{})
	// 用受控阻塞的输出替代磁盘 I/O，保留真正的一代输出入场和释放边界。
	out.output = loggerFunc(func(kratoslog.Level, ...any) error {
		close(entered)
		<-finishWrite
		return nil
	})
	writeDone := make(chan error, 1)
	go func() { writeDone <- out.Log(kratoslog.LevelInfo, "event", "active") }()
	<-entered
	released := make(chan struct{})
	go func() {
		release()
		close(released)
	}()
	select {
	case <-released:
		close(finishWrite)
		<-writeDone
		t.Fatal("release returned before the active write finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(finishWrite)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("release did not finish after the active write")
	}
}
