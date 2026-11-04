package server

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/reporting"
)

func operatingReportFilter(raw string) (reporting.Filter, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return reporting.Filter{}, reporting.ErrInvalidFilter
	}
	for key, value := range values {
		if len(value) != 1 || (key != "from" && key != "to" && key != "tenant_id" && key != "model_id") {
			return reporting.Filter{}, reporting.ErrInvalidFilter
		}
	}
	from, e1 := time.Parse(time.RFC3339Nano, values.Get("from"))
	to, e2 := time.Parse(time.RFC3339Nano, values.Get("to"))
	if e1 != nil || e2 != nil {
		return reporting.Filter{}, reporting.ErrInvalidFilter
	}
	filter := reporting.Filter{From: from.UTC(), To: to.UTC(), TenantID: values.Get("tenant_id"), ModelID: values.Get("model_id")}
	if err = filter.Validate(); err != nil {
		return reporting.Filter{}, err
	}
	return filter, nil
}

func (s *Server) operatingReport(c *gin.Context)    { s.serveOperatingReport(c, false) }
func (s *Server) operatingReportCSV(c *gin.Context) { s.serveOperatingReport(c, true) }

func (s *Server) serveOperatingReport(c *gin.Context, csv bool) {
	c.Header("Cache-Control", "no-store")
	filter, err := operatingReportFilter(c.Request.URL.RawQuery)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid_report_filter", "请提供带时区的起止时间，范围不超过31天，以及有效的租户或模型ID")
		return
	}
	report, err := reporting.Read(c.Request.Context(), s.Pool, filter)
	if err != nil {
		if errors.Is(err, reporting.ErrRangeTooLarge) {
			s.fail(c, http.StatusUnprocessableEntity, "report_range_too_large", "所选范围超过5000个请求，请缩短时间或按租户、模型筛选")
		} else {
			s.fail(c, http.StatusServiceUnavailable, "report_unavailable", "经营报表暂不可用，请稍后重试")
		}
		return
	}
	if !csv {
		s.ok(c, report)
		return
	}
	var body bytes.Buffer
	if err = reporting.WriteCSV(&body, report); err != nil {
		s.fail(c, http.StatusServiceUnavailable, "report_unavailable", "对账文件暂不可用，请稍后重试")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="operations-`+filter.From.Format("20060102")+`-`+filter.To.Format("20060102")+`.csv"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", body.Bytes())
}
