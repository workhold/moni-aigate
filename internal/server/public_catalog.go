package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

type publicCatalogCursor struct {
	Version int    `json:"v"`
	Name    string `json:"name"`
	ID      string `json:"id"`
	Filter  string `json:"filter"`
}

func publicCatalogText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func publicCatalogName(value string) bool {
	return len(value) > 0 && len(value) <= 100 && publicCatalogText(value) && strings.IndexFunc(value, unicode.IsSpace) < 0
}

func publicCatalogFilterDigest(prefix, protocol string) string {
	// Field order and empty values are part of the normalized filter contract.
	raw, _ := json.Marshal(struct {
		Prefix   string `json:"q"`
		Protocol string `json:"protocol"`
	}{prefix, protocol})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func parsePublicCatalogQuery(raw string) (catalog.PublicCatalogFilter, bool) {
	var filter catalog.PublicCatalogFilter
	if len(raw) > 4096 {
		return filter, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return filter, false
	}
	for key, items := range values {
		if (key != "q" && key != "protocol" && key != "cursor") || len(items) != 1 || !publicCatalogText(items[0]) {
			return filter, false
		}
	}
	filter.Prefix = strings.TrimSpace(values.Get("q"))
	filter.Protocol = values.Get("protocol")
	if len(filter.Prefix) > 100 {
		return filter, false
	}
	switch filter.Protocol {
	case "", provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini:
	default:
		return filter, false
	}
	if encoded, present := values["cursor"]; present {
		if encoded[0] == "" || len(encoded[0]) > 1024 {
			return filter, false
		}
		raw, err := base64.RawURLEncoding.DecodeString(encoded[0])
		var cursor publicCatalogCursor
		if err != nil || len(raw) > 768 || base64.RawURLEncoding.EncodeToString(raw) != encoded[0] ||
			!decodeCanonicalJSON(bytes.NewReader(raw), &cursor, 768, "v", "name", "id", "filter") ||
			cursor.Version != 1 || !publicCatalogName(cursor.Name) || !procurementUUID(cursor.ID) ||
			cursor.Filter != publicCatalogFilterDigest(filter.Prefix, filter.Protocol) {
			return filter, false
		}
		filter.AfterName, filter.AfterID = cursor.Name, cursor.ID
	}
	return filter, true
}

func encodePublicCatalogCursor(filter catalog.PublicCatalogFilter, model catalog.PublicModel) string {
	raw, _ := json.Marshal(publicCatalogCursor{Version: 1, Name: model.PublicName, ID: model.ID, Filter: publicCatalogFilterDigest(filter.Prefix, filter.Protocol)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Server) publicCatalogError(c *gin.Context, err error, configuration bool) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		s.fail(c, 404, "not_found", "模型不存在")
	case configuration && errors.Is(err, catalog.ErrPublicCatalogForbidden):
		s.fail(c, 403, "forbidden", "需要平台管理员权限")
	case configuration && errors.Is(err, catalog.ErrPublicCatalogConflict):
		s.fail(c, 409, "public_catalog_revision_conflict", "公开配置已变更，请重新读取后编辑")
	case configuration && errors.Is(err, catalog.ErrPublicCatalogInvalid):
		s.fail(c, 400, "invalid_public_catalog_configuration", "模型公开配置无效")
	default:
		s.fail(c, 503, "public_catalog_unavailable", "公开模型目录暂不可用")
	}
}

func (s *Server) publicModels(c *gin.Context) {
	filter, ok := parsePublicCatalogQuery(c.Request.URL.RawQuery)
	if !ok {
		s.fail(c, 400, "invalid_public_catalog_request", "公开目录查询参数无效")
		return
	}
	page, err := s.Catalog.PublicModels(c.Request.Context(), filter)
	if err != nil {
		s.publicCatalogError(c, err, false)
		return
	}
	next := ""
	if page.HasMore {
		if len(page.Data) == 0 {
			s.publicCatalogError(c, catalog.ErrPublicCatalogUnavailable, false)
			return
		}
		next = encodePublicCatalogCursor(filter, page.Data[len(page.Data)-1])
	}
	c.JSON(200, gin.H{"data": page.Data, "request_id": c.GetString("request_id"), "next_cursor": next, "as_of": page.AsOf})
}

func (s *Server) publicModel(c *gin.Context) {
	if !procurementUUID(c.Param("id")) || c.Request.URL.RawQuery != "" {
		s.fail(c, 400, "invalid_public_catalog_request", "公开目录模型参数无效")
		return
	}
	model, asOf, err := s.Catalog.PublicModel(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.publicCatalogError(c, err, false)
		return
	}
	c.JSON(200, gin.H{"data": model, "request_id": c.GetString("request_id"), "as_of": asOf})
}

type modelPublicationBody struct {
	ExpectedRevision string `json:"expected_revision"`
	PublicVisible    *bool  `json:"public_visible"`
}

func decodeModelPublication(r io.Reader, in *modelPublicationBody) (int64, bool) {
	if !decodeCanonicalJSON(r, in, 8192, "expected_revision", "public_visible") || in.PublicVisible == nil {
		return 0, false
	}
	revision, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	return revision, err == nil && revision > 0 && strconv.FormatInt(revision, 10) == in.ExpectedRevision
}

func (s *Server) modelPublicationID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !procurementUUID(id) || c.Request.URL.RawQuery != "" {
		s.fail(c, 400, "invalid_public_catalog_configuration", "模型公开配置参数无效")
		return "", false
	}
	return id, true
}

func (s *Server) modelPublication(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	id, ok := s.modelPublicationID(c)
	if !ok {
		return
	}
	view, err := s.Catalog.ModelPublication(c.Request.Context(), sessionOf(c).User.ID, id)
	if err != nil {
		s.publicCatalogError(c, err, true)
		return
	}
	s.ok(c, view)
}

func (s *Server) setModelPublication(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	id, ok := s.modelPublicationID(c)
	if !ok {
		return
	}
	var in modelPublicationBody
	revision, ok := decodeModelPublication(c.Request.Body, &in)
	if !ok {
		s.fail(c, 400, "invalid_public_catalog_configuration", "模型公开配置及版本无效")
		return
	}
	view, err := s.Catalog.SetModelPublication(c.Request.Context(), sessionOf(c).User.ID, id, revision, *in.PublicVisible)
	if err != nil {
		s.publicCatalogError(c, err, true)
		return
	}
	s.ok(c, view)
}
