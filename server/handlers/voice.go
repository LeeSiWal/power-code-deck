package handlers

import (
 "context"
 "errors"
 "io"
 "net/http"
 "time"

 "github.com/gorilla/mux"
 "powercodedeck/services"
)

// VoiceRoutes only exposes operations when PowerCodeDeck's own authentication
// is enabled: reverse-proxy auth by itself does not authenticate API callers.
// This is a single-owner MVP, not yet multi-user biometric profile storage.
type VoiceRoutes struct {
 Identity *services.VoiceIdentityService
 Native *services.NativeService
 Agents *services.AgentService
 Transcriber services.VoiceTranscriber
 AuthEnabled bool
}

func (v *VoiceRoutes) status(w http.ResponseWriter, r *http.Request) {
 jsonResponse(w, map[string]any{
  "available": v.AuthEnabled && v.Transcriber != nil,
  "enrolled": v.Identity.Enrolled(),
  "enabled": v.Identity.Enabled(),
  "persistent": false,
 })
}

func (v *VoiceRoutes) requireAuth(w http.ResponseWriter) bool {
 if !v.AuthEnabled {
  jsonError(w,"voice enrollment requires PowerCodeDeck login authentication",http.StatusForbidden)
  return false
 }
 return true
}

func (v *VoiceRoutes) enroll(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) { return }
 if v.Transcriber == nil { jsonError(w,"voice sidecar not configured",http.StatusServiceUnavailable); return }
 r.Body = http.MaxBytesReader(w,r.Body,13*1024*1024)
 if err:=r.ParseMultipartForm(1<<20);err!=nil {jsonError(w,"invalid audio form",400);return}
 if r.FormValue("consent")!="yes" {jsonError(w,"explicit voice-profile consent required",400);return}
 var samples [][]byte
 for _, key := range []string{"sample1","sample2","sample3"} {
  f,_,err:=r.FormFile(key)
  if err!=nil {jsonError(w,"three voice recordings required",400);return}
  b,readErr:=io.ReadAll(io.LimitReader(f,4*1024*1024+1))
  f.Close()
  if readErr!=nil || len(b)<44 || len(b)>4*1024*1024 {jsonError(w,"invalid WAV sample",400);return}
  samples=append(samples,b)
 }
 ctx,cancel:=context.WithTimeout(r.Context(),65*time.Second)
 defer cancel()
 if err:=v.Identity.Enroll(ctx,samples);err!=nil {jsonError(w,"speaker enrollment failed",422);return}
 if err:=v.Identity.Enable();err!=nil {jsonError(w,"speaker protection unavailable",503);return}
 jsonResponse(w,map[string]any{"enrolled":true,"enabled":true,"persistent":false})
}

func (v *VoiceRoutes) command(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) {return}
 if !v.Identity.Enabled() || v.Transcriber==nil {jsonError(w,"voice protection unavailable or not enrolled",403);return}
 id:=mux.Vars(r)["id"]
 if _,err:=v.Agents.Get(id);err!=nil {jsonError(w,"agent not found",404);return}
 r.Body=http.MaxBytesReader(w,r.Body,4*1024*1024+1)
 audio,err:=io.ReadAll(r.Body)
 if err!=nil || len(audio)<44 || len(audio)>4*1024*1024 {jsonError(w,"invalid voice recording",400);return}
 ctx,cancel:=context.WithTimeout(r.Context(),35*time.Second)
 defer cancel()
 ok,err:=v.Identity.Verify(ctx,audio)
 if err!=nil || !ok {
  // A failed verifier never falls back to unauthenticated transcription.
  jsonError(w,"speaker verification failed",403)
  return
 }
 // Text comes from the *same verified audio*, not from client-provided text.
 transcript,err:=v.Transcriber.Transcribe(ctx,audio)
 if err!=nil {jsonError(w,"speech transcription unavailable",503);return}
 if len(transcript)==0 || len([]rune(transcript))>1200 {jsonError(w,"invalid recognized command",422);return}
 // Use existing native driver and permission broker, never execute a shell here.
 if err:=v.Native.Send(id,transcript);err!=nil {
  if errors.Is(err,context.Canceled){jsonError(w,"command cancelled",409);return}
  jsonError(w,"agent unavailable",409);return
 }
 jsonResponse(w,map[string]any{"accepted":true,"transcript":transcript})
}

func (v *VoiceRoutes) delete(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w){return}
 v.Identity.DeleteProfile()
 jsonResponse(w,map[string]any{"enrolled":false,"enabled":false})
}

func RegisterVoiceRoutes(api *mux.Router,v *VoiceRoutes) {
 api.HandleFunc("/voice/status",v.status).Methods(http.MethodGet)
 api.HandleFunc("/voice/enroll",v.enroll).Methods(http.MethodPost)
 api.HandleFunc("/voice/agents/{id}/command",v.command).Methods(http.MethodPost)
 api.HandleFunc("/voice/profile",v.delete).Methods(http.MethodDelete)
}
