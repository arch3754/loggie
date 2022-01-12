package unix

import (
	"bufio"
	"context"
	"fmt"
	"github.com/pkg/errors"
	"golang.org/x/net/netutil"
	"loggie.io/loggie/pkg/core/api"
	"loggie.io/loggie/pkg/core/event"
	"loggie.io/loggie/pkg/core/log"
	"loggie.io/loggie/pkg/pipeline"
	"net"
	"os"
	"time"
)

const Type = "unix"

func init() {
	pipeline.Register(api.SOURCE, Type, makeSource)
}

func makeSource(info pipeline.Info) api.Component {
	return &unix{
		config:    &Config{},
		eventPool: info.EventPool,
		done:      make(chan struct{}),
	}
}

type unix struct {
	name      string
	config    *Config
	done      chan struct{}
	eventPool *event.Pool
}

func (k *unix) Config() interface{} {
	return k.config
}

func (k *unix) Category() api.Category {
	return api.SOURCE
}

func (k *unix) Type() api.Type {
	return Type
}

func (k *unix) String() string {
	return fmt.Sprintf("%s/%s", api.SOURCE, Type)
}

func (k *unix) Init(context api.Context) {
	k.name = context.Name()
}

func (k *unix) Start() {
}

func (k *unix) Stop() {
	log.Info("stopping source unix: %s", k.name)
	close(k.done)
}

func (k *unix) ProductLoop(productFunc api.ProductFunc) {
	log.Info("%s start product loop", k.String())

	if err := checkBind(k.config.Path); err != nil {
		log.Error("check unix sock path error: %+v", err)
		return
	}

	listener, err := net.Listen("unix", k.config.Path)
	if err != nil {
		log.Error("setup unix listener failed: %v", err)
		return
	}
	if k.config.MaxConnections > 0 {
		listener = netutil.LimitListener(listener, k.config.MaxConnections)
	}

	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for {
		select {
		case <-k.done:
			return

		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			log.Warn("unix sock listener accept connection failed: %v", err)
			continue
		}

		go k.handleConn(ctx, conn, productFunc)
	}

}

func (k *unix) handleConn(ctx context.Context, conn net.Conn, productFunc api.ProductFunc) {
	defer conn.Close()

	buf := bufio.NewReader(conn)
	// The split function defaults to ScanLines
	scan := bufio.NewScanner(buf)

	initBuffer := make([]byte, k.config.MaxBytes/4)
	scan.Buffer(initBuffer, k.config.MaxBytes)

	for {
		select {
		case <-ctx.Done():
			return

		default:
		}

		if err := conn.SetDeadline(time.Now().Add(k.config.Timeout)); err != nil {
			log.Warn("set connection timeout error: %v", err)
		}

		if !scan.Scan() {
			if scan.Err() != nil { // close connection when scan error
				log.Warn("scan connection error: %v", scan.Err())
				return
			}

			break
		}

		body := scan.Bytes()
		e := k.eventPool.Get()
		e.Fill(e.Meta(), e.Header(), body)

		productFunc(e)
	}
}

func checkBind(path string) error {
	_, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return errors.WithMessagef(err, "stat path %s failed", path)
	}

	if err := os.Remove(path); err != nil {
		return errors.WithMessagef(err, "remove path %s failed", path)
	}

	return nil
}

func (k *unix) Commit(events []api.Event) {
	k.eventPool.PutAll(events)
}