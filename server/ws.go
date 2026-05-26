package server

import (
	"sync"

	tl "github.com/criteo/consul-timeline/timeline"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const wsChanBuff = 50

type conn struct {
	filter filter
	evts   chan tl.Event
	stop   chan struct{}
}

type ws struct {
	connsLock sync.Mutex
	conns     map[*websocket.Conn]*conn
}

func newWs() *ws {
	return &ws{
		conns: make(map[*websocket.Conn]*conn),
	}
}

func (w *ws) Send(e tl.Event) {
	w.connsLock.Lock()
	defer w.connsLock.Unlock()

	for ws, c := range w.conns {
		select {
		case c.evts <- e:
		default:
			log.Debugf("ws: dropping message for client %s", ws.RemoteAddr())
		}
	}
}

func (w *ws) Add(ws *websocket.Conn, filter filter) {
	w.connsLock.Lock()
	defer w.connsLock.Unlock()

	log.Debugf("ws: adding client connection %s with filter %+v", ws.RemoteAddr(), filter)

	c := &conn{
		filter: filter,
		evts:   make(chan tl.Event, wsChanBuff),
		stop:   make(chan struct{}),
	}

	w.conns[ws] = c

	ws.SetCloseHandler(func(code int, msg string) error {
		w.Remove(ws)
		return nil
	})

	go func() {
		for {
			select {
			case <-c.stop:
				return

			case e := <-c.evts:
				if !c.filter.Match(e) {
					continue
				}

				err := ws.WriteJSON(e)
				if err != nil {
					log.Warnf("ws: client write error: %s", err)
					w.Remove(ws)
					return
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-c.stop:
				return
			default:
			}

			_, _, err := ws.ReadMessage()
			if err != nil {
				return
			}
		}
	}()
}

func (w *ws) Remove(ws *websocket.Conn) {
	w.connsLock.Lock()
	defer w.connsLock.Unlock()

	log.Debugf("ws: removing client connection %s", ws.RemoteAddr())

	state, ok := w.conns[ws]
	if !ok {
		log.Warn("ws: no state found when closing conn, maybe multiple close ?")
		return
	}

	_ = ws.Close()
	close(state.stop)
	delete(w.conns, ws)
}
