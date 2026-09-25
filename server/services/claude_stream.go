// Legacy wire compatibility API. Provider-neutral consumers use internal/providers.
package services

import "powercodedeck/internal/providers/legacywire"

type StreamEvent = legacywire.StreamEvent
type StreamUsage = legacywire.StreamUsage
type MCPServerInfo = legacywire.MCPServerInfo
type PermissionDenial = legacywire.PermissionDenial
type StreamMessage = legacywire.StreamMessage
type ContentBlock = legacywire.ContentBlock
type UserInput = legacywire.UserInput
type UserInputMessage = legacywire.UserInputMessage
type ControlRequest = legacywire.ControlRequest
type ControlRequestBody = legacywire.ControlRequestBody
type ControlResponse = legacywire.ControlResponse

const (
	StreamTypeSystem          = legacywire.StreamTypeSystem
	StreamTypeAssistant       = legacywire.StreamTypeAssistant
	StreamTypeUser            = legacywire.StreamTypeUser
	StreamTypeResult          = legacywire.StreamTypeResult
	StreamTypeRateLimit       = legacywire.StreamTypeRateLimit
	StreamTypeStream          = legacywire.StreamTypeStream
	StreamTypeControlResponse = legacywire.StreamTypeControlResponse
)

func NewInterruptRequest(id string) ControlRequest { return legacywire.NewInterruptRequest(id) }
func NewSetPermissionModeRequest(id, mode string) ControlRequest {
	return legacywire.NewSetPermissionModeRequest(id, mode)
}
func NewUserText(text string) UserInput                  { return legacywire.NewUserText(text) }
func ParseStreamEvent(line []byte) (*StreamEvent, error) { return legacywire.ParseStreamEvent(line) }
