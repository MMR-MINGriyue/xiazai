package controller

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/GopeedLab/gopeed/internal/ratelimit"
	"github.com/GopeedLab/gopeed/pkg/base"
)

type Controller struct {
	GetConfig func(v any)
	GetProxy  func(requestProxy *base.RequestProxy) func(*http.Request) (*url.URL, error)
	FileController
	// Limiter 全局限速器（所有协议共享）；nil 表示不限速。
	Limiter ratelimit.Limiter
}

// SetGlobalRateLimit 设置全局限速（字节/秒）；<=0 关闭限速。
func (c *Controller) SetGlobalRateLimit(bytesPerSec int64) {
	if bytesPerSec <= 0 {
		c.Limiter = nil
		return
	}
	if c.Limiter == nil {
		c.Limiter = ratelimit.NewTokenBucket(bytesPerSec)
	} else {
		c.Limiter.SetRate(bytesPerSec)
	}
}

type FileController interface {
	Touch(name string, size int64) (file *os.File, err error)
}

type DefaultFileController struct {
}

func NewController() *Controller {
	return &Controller{
		GetConfig: func(v any) {},
		GetProxy: func(requestProxy *base.RequestProxy) func(*http.Request) (*url.URL, error) {
			return requestProxy.ToHandler()
		},
		FileController: &DefaultFileController{},
	}
}

func (c *DefaultFileController) Touch(name string, size int64) (file *os.File, err error) {
	dir := filepath.Dir(name)
	if err = os.MkdirAll(dir, os.ModePerm); err != nil {
		return
	}
	file, err = os.Create(name)
	if err != nil {
		return
	}
	if size > 0 {
		err = os.Truncate(name, size)
		if err != nil {
			return nil, err
		}
	}
	return
}

/*func (c *DefaultController) ContextDialer() (proxy.Dialer, error) {
	// return proxy.SOCKS5("tpc", "127.0.0.1:9999", nil, nil)
	var dialer proxy.Dialer
	return &DialerWarp{dialer: dialer}, nil
}

type DialerWarp struct {
	dialer proxy.Dialer
}

type ConnWarp struct {
	conn net.Conn
}

func (c *ConnWarp) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}

func (c *ConnWarp) Write(b []byte) (n int, err error) {
	return c.conn.Write(b)
}

func (c *ConnWarp) Close() error {
	return c.conn.Close()
}

func (c *ConnWarp) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *ConnWarp) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *ConnWarp) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *ConnWarp) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *ConnWarp) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (d *DialerWarp) Dial(network, addr string) (c net.Conn, err error) {
	conn, err := d.dialer.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	return &ConnWarp{conn: conn}, nil
}*/
