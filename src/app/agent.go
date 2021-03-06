/*
接口操作代理
*/
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"sync"
)

// 接口声明
type AgentHandler func(ud interface{}, params interface{}) (interface{}, error)

type AgentSvr struct {
	ln      net.Listener
	db      *Leveldb
	handers map[string][]interface{}
	wg      sync.WaitGroup
}

type Request struct {
	Id     uint32
	Method string
	Params interface{}
}

type Response struct {
	Id     uint32      `json:"id"`
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}

// 分发请求；响应请求命令
func (self *AgentSvr) dispatchRequst(conn *net.TCPConn, req *Request) {
	defer func() {
		if err := recover(); err != nil {
			Error("handle agent connection:%v failed:%v", conn.RemoteAddr(), err)
		}
	}()
	cb, ok := self.handers[req.Method]
	if ok {
		ud := cb[0]
		handler := cb[1].(AgentHandler)
		var resp Response
		resp.Id = req.Id
		if result, err := handler(ud, req.Params); err != nil {
			resp.Error = err
		} else {
			resp.Result = result
		}
		body, err := json.Marshal(resp)
		if err != nil {
			Panic("marshal response conn:%v, failed:%v", conn.RemoteAddr(), err)
		}

		length := uint32(len(body))
		buf := bytes.NewBuffer(nil)
		binary.Write(buf, binary.BigEndian, length)
		buf.Write(body)
		chunk := buf.Bytes()
		if _, err = conn.Write(chunk); err != nil {
			Panic("write response conn:%v, failed:%v", conn.RemoteAddr(), err)
		}
	} else {
		Error("unknown request:%v", req)
	}
}

// 分配连接处理
func (self *AgentSvr) handleConnection(conn *net.TCPConn) {
	defer conn.Close()
	defer self.wg.Done()
	defer func() {
		if err := recover(); err != nil {
			Error("handle agent connection:%v failed:%v", conn.RemoteAddr(), err)
		}
	}()

	Info("new agent connection:%v", conn.RemoteAddr())
	for {
		var sz uint32
		err := binary.Read(conn, binary.BigEndian, &sz)
		if err != nil {
			Error("read conn failed:%v, err:%v", conn.RemoteAddr(), err)
			break
		}
		buf := make([]byte, sz)
		_, err = io.ReadFull(conn, buf)
		if err != nil {
			Error("read conn failed:%v, err:%v", conn.RemoteAddr(), err)
			break
		}
		var req Request
		if err = json.Unmarshal(buf, &req); err != nil {
			Error("parse request failed:%v, err:%v", conn.RemoteAddr(), err)
		}

		go self.dispatchRequst(conn, &req)
	}
}

func (self *AgentSvr) Start() {
	self.wg.Add(1)
	defer self.wg.Done()

	ln, err := net.Listen("tcp", setting.Agent.Addr)
	if err != nil {
		Panic("resolve local addr failed:%s", err.Error())
	}
	Info("start agent succeed:%s", setting.Agent.Addr)

	// register handler
	self.Register("Get", self, handlerGet)

	self.ln = ln
	for {
		conn, err := self.ln.Accept()
		if err != nil {
			Error("accept failed:%v", err)
			if opErr, ok := err.(*net.OpError); ok {
				if !opErr.Temporary() {
					break
				}
			}
			continue
		}
		self.wg.Add(1)
		go self.handleConnection(conn.(*net.TCPConn))
	}
}

func (self *AgentSvr) Stop() {
	if self.ln != nil {
		self.ln.Close()
	}
	self.wg.Wait()
}

// 注册命令
func (self *AgentSvr) Register(cmd string, ud interface{}, handler AgentHandler) {
	self.handers[cmd] = []interface{}{ud, handler}
}

// 查询DB中，指定KEY数据命令
func handlerGet(ud interface{}, params interface{}) (result interface{}, err error) {
	agent := ud.(*AgentSvr)
	key := params.(string)
	Info("agent get:%v", key)
	chunk, err := agent.db.Get([]byte(key))
	if chunk == nil || err != nil {
		Error("query key:%s failed:%v", key, err)
		return
	}
	var data map[string]string
	if err = json.Unmarshal(chunk, &data); err != nil {
		Error("unmarshal key:%s failed:%v", key, err)
		return
	}

	result = data
	return
}

func NewAgent(db *Leveldb) *AgentSvr {
	agent := new(AgentSvr)
	agent.db = db
	agent.handers = make(map[string][]interface{})
	return agent
}
