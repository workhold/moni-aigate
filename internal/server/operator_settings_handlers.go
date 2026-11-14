package server

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func (s *Server) walletOperatorSettings(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	view, err := s.Billing.WalletOperatorSettings(c.Request.Context(), sessionOf(c).User.ID)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, view)
}

func (s *Server) updateWalletOperatorSettings(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	object, ok := commerceObject(c.Request.Body, "operation_id", "expected_revision", "single_operator", "reason", "confirmation")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	operation, operationOK := commerceID(object["operation_id"])
	revision, revisionOK := commerceInteger(object["expected_revision"], true)
	reason, reasonOK := commerceReason(object["reason"])
	confirmation, confirmationOK := commerceString(object["confirmation"])
	var enabled bool
	if !operationOK || !revisionOK || !reasonOK || !confirmationOK || json.Unmarshal(object["single_operator"], &enabled) != nil {
		s.commerceInvalid(c)
		return
	}
	setting, err := s.Billing.UpdateWalletOperatorSetting(c.Request.Context(), billing.WalletOperatorSettingInput{
		OperationID: operation, ActorID: sessionOf(c).User.ID, ExpectedRevision: revision,
		SingleOperator: enabled, Reason: reason, Confirmation: confirmation,
	})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, setting)
}
