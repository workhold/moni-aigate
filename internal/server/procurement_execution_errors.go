package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

func procurementExecutionError(c *gin.Context, s *Server, err error) bool {
	var unknown *procurementexecution.UnknownOutcome
	if errors.As(err, &unknown) {
		s.fail(c, http.StatusServiceUnavailable, unknown.PublicCode(), "提交结果待核对，请凭 request_id 查询结果")
		return true
	}
	var failure *procurementexecution.Failure
	if errors.As(err, &failure) && failure.Domain() == procurementexecution.DomainExecution {
		s.fail(c, http.StatusServiceUnavailable, failure.PublicCode(), "采购执行未完成，请凭 request_id 查询结果")
		return true
	}
	return false
}
