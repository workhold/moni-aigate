package server

import (
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

type modelAliasCreateBody struct {
	OperationID string `json:"operation_id"`
	Alias       string `json:"alias"`
}

type modelAliasRetireBody struct {
	OperationID      string `json:"operation_id"`
	ExpectedRevision string `json:"expected_revision"`
}

func decodeModelAliasCreate(r io.Reader, in *modelAliasCreateBody) bool {
	return decodeCanonicalJSON(r, in, 8192, "operation_id", "alias") && catalog.ValidModelAliasName(in.Alias)
}

func decodeModelAliasRetire(r io.Reader, in *modelAliasRetireBody) (int64, bool) {
	if !decodeCanonicalJSON(r, in, 8192, "operation_id", "expected_revision") {
		return 0, false
	}
	revision, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != in.ExpectedRevision {
		return 0, false
	}
	return revision, true
}

func (s *Server) modelAliasID(c *gin.Context, key string) (string, bool) {
	value := c.Param(key)
	if !catalog.ValidModelAliasName(value) {
		safe := key == "id"
		code, message := "invalid_model_alias", "模型别名参数无效"
		if safe {
			code, message = "invalid_configuration", "模型 ID 参数无效"
		}
		s.fail(c, 400, code, message)
		return "", false
	}
	if key == "id" && !procurementUUID(value) {
		s.fail(c, 400, "invalid_configuration", "模型 ID 参数无效")
		return "", false
	}
	return value, true
}

func (s *Server) modelAliases(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	modelID, ok := s.modelAliasID(c, "id")
	if !ok || c.Request.URL.RawQuery != "" {
		if c.Request.URL.RawQuery != "" {
			s.fail(c, 400, "invalid_model_alias", "模型别名查询参数无效")
		}
		return
	}
	aliases, err := s.Catalog.ModelAliases(c.Request.Context(), modelID)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, aliases)
}

func (s *Server) createModelAlias(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	modelID, ok := s.modelAliasID(c, "id")
	if !ok || c.Request.URL.RawQuery != "" {
		if c.Request.URL.RawQuery != "" {
			s.fail(c, 400, "invalid_model_alias", "模型别名查询参数无效")
		}
		return
	}
	var in modelAliasCreateBody
	if !decodeModelAliasCreate(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_model_alias", "模型别名创建参数无效")
		return
	}
	alias, err := s.Catalog.CreateModelAlias(c.Request.Context(), sessionOf(c).User.ID, modelID, catalog.ModelAliasCreateInput{OperationID: in.OperationID, Alias: in.Alias})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, alias)
}

func (s *Server) retireModelAlias(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	alias, ok := s.modelAliasID(c, "alias")
	if !ok || c.Request.URL.RawQuery != "" {
		if c.Request.URL.RawQuery != "" {
			s.fail(c, 400, "invalid_model_alias", "模型别名查询参数无效")
		}
		return
	}
	var in modelAliasRetireBody
	revision, ok := decodeModelAliasRetire(c.Request.Body, &in)
	if !ok {
		s.fail(c, 400, "invalid_model_alias", "模型别名退役参数无效")
		return
	}
	result, err := s.Catalog.RetireModelAlias(c.Request.Context(), sessionOf(c).User.ID, catalog.ModelAliasRetireInput{OperationID: in.OperationID, Alias: alias, ExpectedRevision: revision})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, result)
}
