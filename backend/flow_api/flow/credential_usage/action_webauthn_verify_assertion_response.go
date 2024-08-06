package credential_usage

import (
	"errors"
	"fmt"
	"github.com/gofrs/uuid"
	auditlog "github.com/teamhanko/hanko/backend/audit_log"
	"github.com/teamhanko/hanko/backend/flow_api/flow/shared"
	"github.com/teamhanko/hanko/backend/flow_api/services"
	"github.com/teamhanko/hanko/backend/flowpilot"
	"github.com/teamhanko/hanko/backend/persistence/models"
)

type WebauthnVerifyAssertionResponse struct {
	shared.Action
}

func (a WebauthnVerifyAssertionResponse) GetName() flowpilot.ActionName {
	return shared.ActionWebauthnVerifyAssertionResponse
}

func (a WebauthnVerifyAssertionResponse) GetDescription() string {
	return "Send the result which was generated by using a webauthn credential."
}

func (a WebauthnVerifyAssertionResponse) Initialize(c flowpilot.InitializationContext) {
	deps := a.GetDeps(c)

	if !c.Stash().Get(shared.StashPathWebauthnAvailable).Bool() || !deps.Cfg.Passkey.Enabled {
		c.SuspendAction()
	}

	c.AddInputs(flowpilot.JSONInput("assertion_response").Required(true))
}

func (a WebauthnVerifyAssertionResponse) Execute(c flowpilot.ExecutionContext) error {
	deps := a.GetDeps(c)

	if valid := c.ValidateInputData(); !valid {
		return c.Error(flowpilot.ErrorFormDataInvalid)
	}

	if !c.Stash().Get(shared.StashPathWebauthnSessionDataID).Exists() {
		return errors.New("webauthn_session_data_id is not present in the stash")
	}

	sessionDataID := uuid.FromStringOrNil(c.Stash().Get(shared.StashPathWebauthnSessionDataID).String())
	assertionResponse := c.Input().Get("assertion_response").String()

	params := services.VerifyAssertionResponseParams{
		Tx:                deps.Tx,
		SessionDataID:     sessionDataID,
		AssertionResponse: assertionResponse,
	}

	userModel, err := deps.WebauthnService.VerifyAssertionResponse(params)
	if err != nil {
		if errors.Is(err, services.ErrInvalidWebauthnCredential) {
			err = deps.AuditLogger.CreateWithConnection(
				deps.Tx,
				deps.HttpContext,
				models.AuditLogLoginFailure,
				userModel,
				err,
				auditlog.Detail("login_method", "passkey"),
				auditlog.Detail("flow_id", c.GetFlowID()))

			if err != nil {
				return fmt.Errorf("could not create audit log: %w", err)
			}

			return c.Error(shared.ErrorPasskeyInvalid.Wrap(err))
		}

		return fmt.Errorf("failed to verify assertion response: %w", err)
	}

	err = c.Stash().Set(shared.StashPathUserID, userModel.ID.String())
	if err != nil {
		return fmt.Errorf("failed to set user_id to the stash: %w", err)
	}

	// Set only for audit logging purposes.
	err = c.Stash().Set(shared.StashPathLoginMethod, "passkey")
	if err != nil {
		return fmt.Errorf("failed to set login_method to the stash: %w", err)
	}

	if userModel != nil {
		_ = c.Stash().Set(shared.StashPathUserHasPassword, userModel.PasswordCredential != nil)
		_ = c.Stash().Set(shared.StashPathUserHasWebauthnCredential, len(userModel.WebauthnCredentials) > 0)
		_ = c.Stash().Set(shared.StashPathUserHasUsername, len(userModel.GetUsername()) > 0)
		_ = c.Stash().Set(shared.StashPathUserHasEmails, len(userModel.Emails) > 0)
	}

	c.PreventRevert()

	return c.Continue()
}
