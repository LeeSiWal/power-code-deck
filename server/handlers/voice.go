package handlers

import (
 "context"
 "io"
 "net/http"
 "time"

 "github.com/gorilla/mux"
 "powercodedeck/services"
)

// VoiceRoutes exposes a non-executing voice profile API. No endpoint in this
// router can send text to Claude, Codex, a shell or a Git operation.
// Actual command authorization remains the existing authenticated UI approval
// flow. Speaker similarity is not strong identity authentication.
type VoiceRoutes struct {
 Identity *services.VoiceIdentityService
 AuthEnabled bool
}

func (v *VoiceRoutes) available() bool {
 return v.AuthEnabled && v.Identity != nil
}

func (v *VoiceRoutes) requireAuth(w http.ResponseWriter) bool {
 if !v.available() {
  jsonError(w,"voice identity requires enabled PowerCodeDeck account authentication",http.StatusForbidden)
  return false
 }
 return true
}

func (v *VoiceRoutes) status(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) {return}
 jsonResponse(w,map[string]any{
  "available":true,
  "enrolled":v.Identity.Enrolled(),
  "enabled":v.Identity.Enabled(),
  "persistent":false,
  "commandGate":false,
 })
}

// enroll accepts three consented WAV recordings. Enrollment templates currently
// live only in memory; they disappear when the server restarts. This endpoint
// intentionally does not store raw recordings or expose embeddings to callers.
func (v *VoiceRoutes) enroll(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) {return}
 r.Body=http.MaxBytesReader(w,r.Body,13*1024*1024)
 if err:=r.ParseMultipartForm(1<<20);err!=nil {jsonError(w,"invalid audio form",http.StatusBadRequest);return}
 defer r.MultipartForm.RemoveAll()
 if r.FormValue("consent")!="yes" {jsonError(w,"explicit voice-profile consent required",http.StatusBadRequest);return}
 samples:=make([][]byte,0,3)
 for _,key:=range []string{"sample1","sample2","sample3"} {
  file,_,err:=r.FormFile(key)
  if err!=nil {jsonError(w,"three WAV samples are required",http.StatusBadRequest);return}
  data,readErr:=io.ReadAll(io.LimitReader(file,4*1024*1024+1))
  file.Close()
  if readErr!=nil || len(data)<44 || len(data)>4*1024*1024 {
   jsonError(w,"invalid sample size",http.StatusBadRequest);return
  }
  // Reject obvious non-WAV bodies before forwarding data to the local model.
  if string(data[:4])!="RIFF" || string(data[8:12])!="WAVE" {
   jsonError(w,"samples must be WAV",http.StatusUnsupportedMediaType);return
  }
  samples=append(samples,data)
 }
 ctx,cancel:=context.WithTimeout(r.Context(),65*time.Second)
 defer cancel()
 if err:=v.Identity.Enroll(ctx,samples);err!=nil {
  jsonError(w,"speaker enrollment could not be completed",http.StatusUnprocessableEntity);return
 }
 if err:=v.Identity.Enable();err!=nil {
  jsonError(w,"speaker verification unavailable",http.StatusServiceUnavailable);return
 }
 jsonResponse(w,map[string]any{"enrolled":true,"enabled":true,"persistent":false})
}

// verify is informational only: passing this check NEVER grants execution
// rights to an agent. API callers cannot use a "verified" result to authorize
// code edits, and the server does not accept client-supplied transcript here.
func (v *VoiceRoutes) verify(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) {return}
 r.Body=http.MaxBytesReader(w,r.Body,4*1024*1024+1)
 audio,err:=io.ReadAll(r.Body)
 if err!=nil || len(audio)<44 || len(audio)>4*1024*1024 {
  jsonError(w,"invalid audio sample",http.StatusBadRequest);return
 }
 if string(audio[:4])!="RIFF" || string(audio[8:12])!="WAVE" {
  jsonError(w,"WAV audio required",http.StatusUnsupportedMediaType);return
 }
 ctx,cancel:=context.WithTimeout(r.Context(),20*time.Second)
 defer cancel()
 ok,err:=v.Identity.Verify(ctx,audio)
 if err!=nil || !ok {
  // Failure does not disclose confidence scores or templates.
  jsonResponse(w,map[string]any{"verified":false,"canExecute":false})
  return
 }
 jsonResponse(w,map[string]any{"verified":true,"canExecute":false})
}

func (v *VoiceRoutes) delete(w http.ResponseWriter,r *http.Request) {
 if !v.requireAuth(w) {return}
 v.Identity.DeleteProfile()
 jsonResponse(w,map[string]any{"enrolled":false,"enabled":false})
}

func RegisterVoiceRoutes(api *mux.Router,v *VoiceRoutes) {
 api.HandleFunc("/voice/status",v.status).Methods(http.MethodGet)
 api.HandleFunc("/voice/enroll",v.enroll).Methods(http.MethodPost)
 api.HandleFunc("/voice/verify",v.verify).Methods(http.MethodPost)
 api.HandleFunc("/voice/profile",v.delete).Methods(http.MethodDelete)
}
