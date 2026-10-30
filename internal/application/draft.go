package application

import (
	"encoding/json"
	"errors"

	"agent-task-bridge/internal/config"
	"agent-task-bridge/internal/domain"
	"agent-task-bridge/internal/ports"
)

// A draft retains the creation envelope even if the issuer capability is later
// changed. Editing rights do not create a route around the original authority.
type draftBoundary struct {
	OwnerCreated bool     `json:"owner_created"`
	Eligible     []string `json:"eligible"`
	Acceptance   []string `json:"acceptance"`
}

func saveDraftBoundary(tx ports.Transaction, c config.Config, ids []string, taskID string, ownerCreated bool) error {
	b := draftBoundary{}
	if len(ids) != 1 {
		return errors.New("creation authorization is missing")
	}
	if ownerCreated {
		b.OwnerCreated = true
	} else {
		found := false
		for _, cap := range c.OwnerCapabilities {
			if cap.ID == ids[0] {
				b.Eligible, b.Acceptance, found = cap.EligibleAgentIDs, cap.AcceptancePrincipalIDs, true
				break
			}
		}
		if !found {
			return errors.New("creation capability is missing")
		}
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	return tx.SetMeta("draft_boundary:"+taskID, string(raw))
}

func checkDraftBoundary(tx ports.Transaction, taskID string, c domain.Contract) error {
	raw, e := tx.Meta("draft_boundary:" + taskID)
	if e != nil {
		return e
	}
	var b draftBoundary
	if e = json.Unmarshal([]byte(raw), &b); e != nil {
		return e
	}
	if b.OwnerCreated {
		return nil
	}
	for _, a := range c.EligibleAgentIDs {
		if !domain.Contains(b.Eligible, a) {
			return domain.E("POLICY_DENIED")
		}
	}
	if !domain.Contains(b.Acceptance, c.AcceptancePrincipalID) {
		return domain.E("POLICY_DENIED")
	}
	return nil
}
