package server

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func (s *Server) coinDisplay(c *gin.Context) {
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	view, err := s.Billing.CoinDisplayCurrent(c.Request.Context())
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, view)
}

func (s *Server) coinDisplaySettings(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	view, err := s.Billing.CoinDisplaySettings(c.Request.Context(), sessionOf(c).User.ID)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, view)
}

func (s *Server) coinDisplayOperation(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	operation := c.Param("operationID")
	if !procurementUUID(operation) {
		s.commerceInvalid(c)
		return
	}
	view, err := s.Billing.CoinDisplayOperation(c.Request.Context(), sessionOf(c).User.ID, operation)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, view)
}

func nullableCoinDisplayString(raw json.RawMessage) (string, bool, bool) {
	if string(raw) == "null" {
		return "", false, true
	}
	value, ok := commerceString(raw)
	return value, true, ok
}

func (s *Server) updateCoinDisplay(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	object, ok := commerceObject(c.Request.Body, "operation_id", "expected_revision", "display_name", "fiat_currency", "rate_num", "rate_den", "reason", "confirmation")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	operation, operationOK := commerceID(object["operation_id"])
	revision, revisionOK := commerceInteger(object["expected_revision"], true)
	displayName, nameOK := commerceString(object["display_name"])
	currency, currencyPresent, currencyOK := nullableCoinDisplayString(object["fiat_currency"])
	_, numeratorPresent, numeratorOK := nullableCoinDisplayString(object["rate_num"])
	_, denominatorPresent, denominatorOK := nullableCoinDisplayString(object["rate_den"])
	reason, reasonOK := commerceReason(object["reason"])
	confirmation, confirmationOK := commerceString(object["confirmation"])
	if !operationOK || !revisionOK || !nameOK || !currencyOK || !numeratorOK || !denominatorOK || !reasonOK || !confirmationOK || currencyPresent != numeratorPresent || currencyPresent != denominatorPresent {
		s.commerceInvalid(c)
		return
	}
	var rateNum, rateDen int64
	if currencyPresent {
		var numOK, denOK bool
		rateNum, numOK = commerceInteger(object["rate_num"], true)
		rateDen, denOK = commerceInteger(object["rate_den"], true)
		if !numOK || !denOK {
			s.commerceInvalid(c)
			return
		}
	}
	setting, err := s.Billing.UpdateCoinDisplay(c.Request.Context(), billing.CoinDisplayInput{
		OperationID: operation, ActorID: sessionOf(c).User.ID, ExpectedRevision: revision,
		DisplayName: displayName, FiatCurrency: currency, RateNum: rateNum, RateDen: rateDen,
		Reason: reason, Confirmation: confirmation,
	})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, setting)
}
