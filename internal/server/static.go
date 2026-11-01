package server

import (
	"github.com/gin-gonic/gin"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

func (s *Server) static(c *gin.Context) {
	p := c.Request.URL.Path
	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/") || c.Request.Method != "GET" {
		s.fail(c, 404, "not_found", "接口不存在")
		return
	}
	if s.assets == nil {
		s.fail(c, 503, "console_unavailable", "请先构建控制台")
		return
	}
	clean := strings.TrimPrefix(path.Clean("/"+p), "/")
	if clean == "." || clean == "" {
		clean = "index.html"
	}
	if b, e := fs.ReadFile(s.assets, clean); e == nil {
		mime := "application/octet-stream"
		switch path.Ext(clean) {
		case ".html":
			mime = "text/html; charset=utf-8"
		case ".js":
			mime = "text/javascript; charset=utf-8"
		case ".css":
			mime = "text/css; charset=utf-8"
		case ".svg":
			mime = "image/svg+xml"
		case ".ico":
			mime = "image/x-icon"
		}
		c.Data(200, mime, b)
		return
	}
	if path.Ext(clean) != "" {
		c.Status(http.StatusNotFound)
		return
	}
	b, e := fs.ReadFile(s.assets, "index.html")
	if e != nil {
		s.fail(c, 503, "console_unavailable", "请先构建控制台")
		return
	}
	c.Data(200, "text/html; charset=utf-8", b)
}
