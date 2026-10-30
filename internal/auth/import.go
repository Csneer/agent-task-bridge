package auth

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"agent-task-bridge/internal/config"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// PrepareImport validates a full declarative configuration update. The returned
// value is safe to persist: bearer values are replaced by hashes, omitted
// identities remain disabled, and revoked authorization instances retain their
// tombstones. The caller must commit this result and auth_epoch atomically.
func PrepareImport(old *config.Config, desired config.Config, now time.Time) (config.Config, error) {
	raw, err := json.Marshal(desired)
	if err != nil {
		return config.Config{}, err
	}
	var result config.Config
	if err := json.Unmarshal(raw, &result); err != nil {
		return config.Config{}, err
	}
	if !identifier.MatchString(result.AuthorityID) || (old != nil && old.AuthorityID != result.AuthorityID) {
		return config.Config{}, fmt.Errorf("authority identity mismatch or invalid identifier")
	}
	principals := make(map[string]bool)
	for _, principal := range result.Principals {
		if err := uniqueID(principals, principal.PrincipalID); err != nil {
			return config.Config{}, err
		}
	}
	agents := make(map[string]bool)
	for _, agent := range result.Agents {
		if err := uniqueID(agents, agent.AgentID); err != nil {
			return config.Config{}, err
		}
	}
	if old != nil {
		for _, principal := range old.Principals {
			if !principals[principal.PrincipalID] {
				principal.Enabled = false
				result.Principals = append(result.Principals, principal)
				principals[principal.PrincipalID] = true
			}
		}
		for _, agent := range old.Agents {
			if !agents[agent.AgentID] {
				agent.Enabled = false
				result.Agents = append(result.Agents, agent)
				agents[agent.AgentID] = true
			}
		}
	}
	oldBindings := make(map[string]config.Binding)
	oldCredentials := make(map[string]config.Credential)
	oldCapabilities := make(map[string]config.OwnerCapability)
	if old != nil {
		for _, binding := range old.Bindings {
			oldBindings[binding.BindingID] = binding
		}
		for _, credential := range old.Credentials {
			oldCredentials[credential.CredentialID] = credential
		}
		for _, capability := range old.OwnerCapabilities {
			oldCapabilities[capability.ID] = capability
		}
	}
	ids := make(map[string]bool)
	for i := range result.Bindings {
		binding := &result.Bindings[i]
		if err := uniqueID(ids, binding.BindingID); err != nil {
			return config.Config{}, err
		}
		if !principals[binding.PrincipalID] || !agents[binding.AgentID] {
			return config.Config{}, fmt.Errorf("binding references unknown identity")
		}
		if _, _, ok := interval(binding.NotBefore, binding.ExpiresAt); !ok {
			return config.Config{}, fmt.Errorf("invalid binding interval")
		}
		if err := checkRevocation(binding.RevokedAt, now); err != nil {
			return config.Config{}, err
		}
		if prior, exists := oldBindings[binding.BindingID]; exists {
			if prior.PrincipalID != binding.PrincipalID || prior.AgentID != binding.AgentID || prior.NotBefore != binding.NotBefore || prior.ExpiresAt != binding.ExpiresAt {
				return config.Config{}, fmt.Errorf("binding instance is immutable; create a new binding ID")
			}
			if err := preserveRevocation(prior.RevokedAt, binding.RevokedAt); err != nil {
				return config.Config{}, err
			}
		}
		if binding.RevokedAt == nil && (!principalEnabled(result, binding.PrincipalID) || !agentEnabled(result, binding.AgentID)) {
			binding.RevokedAt = revokedAtNow(now)
		}
	}
	for id, binding := range oldBindings {
		if !ids[id] {
			if binding.RevokedAt == nil {
				binding.RevokedAt = revokedAtNow(now)
			}
			result.Bindings = append(result.Bindings, binding)
		}
	}
	// Reject future overlap as well, rather than admitting a configuration that
	// silently develops two current bindings when a scheduled period starts.
	for i, left := range result.Bindings {
		if left.RevokedAt != nil {
			continue
		}
		fromLeft, toLeft, _ := interval(left.NotBefore, left.ExpiresAt)
		for _, right := range result.Bindings[i+1:] {
			if right.RevokedAt != nil || left.PrincipalID != right.PrincipalID || left.AgentID != right.AgentID {
				continue
			}
			fromRight, toRight, _ := interval(right.NotBefore, right.ExpiresAt)
			if fromLeft.Before(toRight) && fromRight.Before(toLeft) {
				return config.Config{}, fmt.Errorf("overlapping binding instances for one Principal-Agent pair")
			}
		}
	}
	ids = make(map[string]bool)
	tokenHashes := make(map[string]bool)
	for i := range result.Credentials {
		credential := &result.Credentials[i]
		if err := uniqueID(ids, credential.CredentialID); err != nil {
			return config.Config{}, err
		}
		if !principals[credential.PrincipalID] {
			return config.Config{}, fmt.Errorf("credential references unknown Principal")
		}
		if _, _, ok := interval(credential.NotBefore, credential.ExpiresAt); !ok {
			return config.Config{}, fmt.Errorf("invalid credential interval")
		}
		if err := checkRevocation(credential.RevokedAt, now); err != nil {
			return config.Config{}, err
		}
		if credential.Token != "" {
			digest := TokenHash(credential.Token)
			if credential.TokenHash != "" && credential.TokenHash != digest {
				return config.Config{}, fmt.Errorf("credential hash does not match supplied bearer")
			}
			credential.TokenHash = digest
			credential.Token = ""
		}
		if prior, exists := oldCredentials[credential.CredentialID]; exists {
			if credential.TokenHash == "" {
				credential.TokenHash = prior.TokenHash
			}
			if prior.PrincipalID != credential.PrincipalID || prior.TokenHash != credential.TokenHash || prior.NotBefore != credential.NotBefore || prior.ExpiresAt != credential.ExpiresAt {
				return config.Config{}, fmt.Errorf("credential instance is immutable; create a new credential ID")
			}
			if err := preserveRevocation(prior.RevokedAt, credential.RevokedAt); err != nil {
				return config.Config{}, err
			}
		}
		if !safeHash(credential.TokenHash) || tokenHashes[credential.TokenHash] {
			return config.Config{}, fmt.Errorf("invalid or reused credential hash")
		}
		tokenHashes[credential.TokenHash] = true
	}
	for id, credential := range oldCredentials {
		if !ids[id] {
			if tokenHashes[credential.TokenHash] {
				return config.Config{}, fmt.Errorf("credential bearer cannot be reassigned to a new ID")
			}
			if credential.RevokedAt == nil {
				credential.RevokedAt = revokedAtNow(now)
			}
			result.Credentials = append(result.Credentials, credential)
		}
	}
	ids = make(map[string]bool)
	for i := range result.OwnerCapabilities {
		capability := &result.OwnerCapabilities[i]
		if err := uniqueID(ids, capability.ID); err != nil {
			return config.Config{}, err
		}
		if !principals[capability.OwnerPrincipalID] || !principals[capability.SubjectPrincipalID] {
			return config.Config{}, fmt.Errorf("capability references unknown Principal")
		}
		if _, _, ok := interval(capability.NotBefore, capability.ExpiresAt); !ok {
			return config.Config{}, fmt.Errorf("invalid capability interval")
		}
		if err := checkRevocation(capability.RevokedAt, now); err != nil {
			return config.Config{}, err
		}
		if prior, exists := oldCapabilities[capability.ID]; exists {
			if prior.OwnerPrincipalID != capability.OwnerPrincipalID || prior.SubjectPrincipalID != capability.SubjectPrincipalID {
				return config.Config{}, fmt.Errorf("capability identity is immutable")
			}
			if err := preserveRevocation(prior.RevokedAt, capability.RevokedAt); err != nil {
				return config.Config{}, err
			}
		}
		for _, action := range capability.Actions {
			if !taskActions[action] && action != "task.create" && action != "grant.issue" && action != "grant.revoke" {
				return config.Config{}, fmt.Errorf("unknown capability action")
			}
		}
		for _, action := range capability.GrantActions {
			if !taskActions[action] {
				return config.Config{}, fmt.Errorf("capability cannot issue recursive or unknown actions")
			}
		}
		for _, subject := range append(append([]string{}, capability.GrantSubjects...), capability.AcceptancePrincipalIDs...) {
			if !principals[subject] {
				return config.Config{}, fmt.Errorf("capability envelope references unknown Principal")
			}
		}
		for _, agent := range capability.EligibleAgentIDs {
			if !agents[agent] {
				return config.Config{}, fmt.Errorf("capability envelope references unknown Agent")
			}
		}
		if contains(capability.Actions, "grant.issue") {
			deadline, err := time.Parse(timestampLayout, capability.MaxGrantExpiresAt)
			if err != nil || deadline.Format(timestampLayout) != capability.MaxGrantExpiresAt {
				return config.Config{}, fmt.Errorf("grant issuer requires a valid maximum deadline")
			}
		}
	}
	for id, capability := range oldCapabilities {
		if !ids[id] {
			if capability.RevokedAt == nil {
				capability.RevokedAt = revokedAtNow(now)
			}
			result.OwnerCapabilities = append(result.OwnerCapabilities, capability)
		}
	}
	sortConfig(&result)
	return result, nil
}

func uniqueID(seen map[string]bool, id string) error {
	if !identifier.MatchString(id) || seen[id] {
		return fmt.Errorf("invalid or duplicate configuration identifier")
	}
	seen[id] = true
	return nil
}

func preserveRevocation(old, desired *string) error {
	if old != nil && (desired == nil || *desired != *old) {
		return fmt.Errorf("revoked authorization instance cannot be restored or rewritten")
	}
	return nil
}

func checkRevocation(value *string, now time.Time) error {
	if value == nil {
		return nil
	}
	point, err := time.Parse(timestampLayout, *value)
	if err != nil || point.Format(timestampLayout) != *value || point.After(now) {
		return fmt.Errorf("revocation must be a valid current or past server timestamp")
	}
	return nil
}
