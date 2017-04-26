package anet

import (
	"log"
	"net"
	"time"
)

type Session struct {
	conn          *net.TCPConn
	proto         Protocol
	wbuf          chan interface{}
	events        chan Event
	ctrl          chan bool
	net           string
	raddr         *net.TCPAddr
	autoReconnect bool
	reconnect     chan bool
}

const (
	SEND_BUFF_SIZE   = 1024
	CONNECT_INTERVAL = 1000 // reconnect interval
)

func newSession(conn *net.TCPConn, proto Protocol) *Session {
	sess := &Session{
		conn:          conn,
		proto:         proto,
		wbuf:          make(chan interface{}, SEND_BUFF_SIZE),
		events:        nil,
		ctrl:          make(chan bool, 1),
		net:           "",
		raddr:         nil,
		autoReconnect: false,
		reconnect:     nil,
	}
	return sess
}

func ConnectTo(network string, addr string, proto Protocol, events chan Event, autoReconnect bool) *Session {
	session := newSession(nil, proto)
	session.connect(network, addr, events, autoReconnect)
	return session
}

// only call it when without autoreconnect
func (self *Session) Start(events chan Event) {
	self.events = events
	go self.reader()
	go self.writer()
}

func (self *Session) Close() {
	if self.autoReconnect {
		self.reconnect <- false
	}
	self.conn.Close()
}

func (self *Session) Send(payload interface{}) {
	defer func() {
		if err := recover(); err != nil {
			log.Println("send: ", err)
		}
	}()

	if len(self.wbuf) < SEND_BUFF_SIZE {
		self.wbuf <- payload
	} else {
		log.Println("send overflow")
	}
}

func (self *Session) reader() {
	log.Printf("session[%v] start reader...", self)
	defer func() {
		log.Println("reader quit...")
		self.ctrl <- true
		if self.autoReconnect {
			self.reconnect <- true
		} else {
			self.events <- newEvent(EVENT_DISCONNECT, self, nil)
		}
	}()
	for {
		msg, err := self.proto.Read(self.conn)
		if err != nil {
			self.events <- newEvent(EVENT_RECV_ERROR, self, err)
			break
		}
		self.events <- newEvent(EVENT_MESSAGE, self, msg)
	}
}

func (self *Session) writer() {
	log.Printf("session[%v] start writer...", self)
	defer func() {
		log.Println("writer quit ...")
		close(self.wbuf)
		self.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-self.wbuf:
			if ok {
				if err := self.proto.Write(self.conn, msg); err != nil {
					self.events <- newEvent(EVENT_SEND_ERROR, self, err)
					return
				}
			} else {
				return
			}
		case <-self.ctrl:
			return
		}
	}
}

func (self *Session) supervisor() {
	defer func() {
		log.Println("supervisor quit...")
	}()
	for {
		select {
		case flag, ok := <-self.reconnect:
			if ok {
				if flag {
					log.Printf("reconnect to %s", self.raddr)
					go self.connector()
				} else {
					return
				}
			}
		}
	}
}

func (self *Session) connect(network string, addr string, events chan Event, autoReconnect bool) error {
	log.Printf("try to connect to %s %s", network, addr)
	raddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return err
	}
	self.events = events
	self.net = network
	self.raddr = raddr
	if autoReconnect {
		self.autoReconnect = autoReconnect
		self.reconnect = make(chan bool, 1)
		go self.supervisor()
	}
	go self.connector()
	return nil
}

func (self *Session) connector() {
	conn, err := net.DialTCP(self.net, nil, self.raddr)
	if err != nil {
		if self.autoReconnect {
			time.Sleep(CONNECT_INTERVAL * time.Millisecond)
			self.reconnect <- true
		} else {
			self.events <- newEvent(EVENT_CONNECT_FAILED, self, err)
		}
	} else {
		log.Printf("connect to %s ok...session=%v", self.raddr, self)
		self.conn = conn
		if !self.autoReconnect {
			self.events <- newEvent(EVENT_CONNECT_SUCCESS, self, nil)
		} else {
			self.wbuf = make(chan interface{}, SEND_BUFF_SIZE)
			self.Start(self.events)
		}
	}
}

func (self *Session) RemoteAddr() string {
	if self.raddr == nil {
		return ""
	}
	return self.raddr.String()
}
