// Package bridge exposes the daemon application and controlled local test seams.
// Agents use HTTP; this API is for embedding, trusted configuration, and tests.
package bridge

import "agent-task-bridge/internal/application"
import "agent-task-bridge/internal/config"

type Service = application.Service
type Options = application.Options
type HookEvent = application.HookEvent
type Config = config.Config
type Principal = config.Principal
type Agent = config.Agent
type Binding = config.Binding
type Credential = config.Credential
type OwnerCapability = config.OwnerCapability

var Open = application.Open
