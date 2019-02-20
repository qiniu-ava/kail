package kail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	lifecycle "github.com/boz/go-lifecycle"
	logutil "github.com/boz/go-logutil"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const (
	logBufsiz          = 1024 * 16 // 16k max message size
	monitorDeliverWait = time.Millisecond
)

var (
	canaryLog = []byte("unexpected stream type \"\"")
)

type monitorConfig struct {
	since time.Duration
}

type monitor interface {
	Shutdown()
	Done() <-chan struct{}
}

func newMonitor(c *controller, source EventSource, config monitorConfig) monitor {
	lc := lifecycle.New()
	go lc.WatchContext(c.ctx)

	log := c.log.WithComponent(
		fmt.Sprintf("monitor [%v]", source))

	m := &_monitor{
		rc:      c.rc,
		source:  source,
		config:  config,
		eventch: c.eventch,
		log:     log,
		lc:      lc,
		ctx:     c.ctx,
	}

	go m.run()

	return m
}

type _monitor struct {
	rc      *rest.Config
	source  EventSource
	config  monitorConfig
	eventch chan<- Event
	log     logutil.Log
	lc      lifecycle.Lifecycle
	ctx     context.Context
}

func (m *_monitor) Shutdown() {
	m.lc.ShutdownAsync(nil)
}

func (m *_monitor) Done() <-chan struct{} {
	return m.lc.Done()
}

func (m *_monitor) run() {
	defer m.log.Un(m.log.Trace("run"))
	defer m.lc.ShutdownCompleted()

	ctx, cancel := context.WithCancel(m.ctx)

	client, err := m.makeClient(ctx)
	if err != nil {
		m.lc.ShutdownInitiated(err)
		cancel()
		return
	}

	donech := make(chan struct{})

	go m.mainloop(ctx, client, donech)

	err = <-m.lc.ShutdownRequest()
	m.lc.ShutdownInitiated(err)
	cancel()

	<-donech
}

func (m *_monitor) makeClient(ctx context.Context) (corev1.CoreV1Interface, error) {
	cs, err := kubernetes.NewForConfig(m.rc)
	if err != nil {
		return nil, err
	}
	return cs.CoreV1(), nil
}

func (m *_monitor) mainloop(
	ctx context.Context, client corev1.CoreV1Interface, donech chan struct{}) {
	defer m.log.Un(m.log.Trace("mainloop"))
	defer close(donech)

	// todo: backoff handled by k8 client?

	sinceSecs := int64(m.config.since / time.Second)
	since := &sinceSecs
	var sinceTime *metav1.Time
	var err error

	m.log.Debugf("displaying logs since %v seconds", sinceSecs)

	for i := 0; ctx.Err() == nil; i++ {

		m.log.Debugf("readloop count: %v", i)

		sinceTime, err = m.readloop(ctx, client, since, sinceTime)
		switch {
		case err == io.EOF:
		case err == io.ErrUnexpectedEOF: // retry for disconnection
		case err == nil:
		case ctx.Err() != nil:
			m.lc.ShutdownAsync(nil)
			return
		default:
			m.log.ErrWarn(err, "streaming done")
			m.lc.ShutdownAsync(err)
			return
		}
	}
}

func (m *_monitor) readloop(
	ctx context.Context, client corev1.CoreV1Interface, sinceSeconds *int64, sinceTime *metav1.Time) (lastSuccessTime *metav1.Time, err error) {

	defer m.log.Un(m.log.Trace("readloop"))

	opts := &v1.PodLogOptions{
		Container: m.source.Container(),
		Follow:    true,
	}
	if sinceTime != nil { // use sinceTime firstly
		opts.SinceTime = sinceTime
	} else if sinceSeconds != nil {
		opts.SinceSeconds = sinceSeconds
	}

	req := client.
		Pods(m.source.Namespace()).
		GetLogs(m.source.Name(), opts).
		Context(ctx)

	stream, e := req.Stream()
	if e != nil {
		m.log.ErrWarn(e, "error while connecting log stream")
		err = io.ErrUnexpectedEOF
		return
	}

	defer stream.Close()

	logbuf := make([]byte, logBufsiz)
	buffer := newBuffer(m.source)

	for ctx.Err() == nil {
		nread, e := stream.Read(logbuf)

		switch {
		case e == io.EOF:
			err = e
			return
		case ctx.Err() != nil:
			err = ctx.Err()
			return
		case e != nil:
			m.log.ErrWarn(e, "error while reading log stream")
			err = io.ErrUnexpectedEOF
			return
		case nread == 0:
			err = io.EOF
			return
		}

		log := logbuf[0:nread]

		if bytes.Compare(canaryLog, log) == 0 {
			m.log.Debugf("received 'unexpect stream type'")
			continue
		}

		if events := buffer.process(log); len(events) > 0 {
			m.deliverEvents(ctx, events)
		}

		now := metav1.Time{Time: time.Now().Add(time.Second)}
		lastSuccessTime = &now
	}
	return
}

func (m *_monitor) deliverEvents(ctx context.Context, events []Event) {
	t := time.NewTimer(monitorDeliverWait)
	defer t.Stop()

	for i, event := range events {
		select {
		case m.eventch <- event:
		case <-t.C:
			m.log.Warnf("event buffer full. dropping %v logs", len(events)-i)
			return
		case <-ctx.Done():
			return
		}
	}
}
