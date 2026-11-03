package handlers

import (
 "bytes"
 "context"
 "encoding/json"
 "mime/multipart"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/gorilla/mux"
 "powercodedeck/services"
)

type fakeVoiceVerifier struct {
 match bool
}
func (f *fakeVoiceVerifier) Enroll(_ context.Context, _ [][]byte) ([]byte,error) {
 return []byte("test-speaker-template"),nil
}
func (f *fakeVoiceVerifier) Match(_ context.Context,_,_ []byte)(bool,error){
 return f.match,nil
}
func wavSample() []byte {
 b:=make([]byte,64)
 copy(b[0:4],[]byte("RIFF"))
 copy(b[8:12],[]byte("WAVE"))
 return b
}
func voiceTestRouter(v *VoiceRoutes)*mux.Router{
 r:=mux.NewRouter()
 RegisterVoiceRoutes(r,v)
 // Production mount is /api, but handler tests focus on the route contract.
 return r
}
func TestVoiceRouteRequiresAppAuth(t *testing.T) {
 id:=services.NewVoiceIdentityService(&fakeVoiceVerifier{match:true})
 route:=voiceTestRouter(&VoiceRoutes{Identity:id,AuthEnabled:false})
 for _,endpoint:=range []struct{method,path string}{
  {"GET","/voice/status"},{"POST","/voice/enroll"},
  {"POST","/voice/verify"},{"DELETE","/voice/profile"},
 }{
  request:=httptest.NewRequest(endpoint.method,endpoint.path,nil)
  response:=httptest.NewRecorder()
  route.ServeHTTP(response,request)
  if response.Code!=http.StatusForbidden{t.Fatalf("%s: expected 403, got %d",endpoint.path,response.Code)}
 }
}
func TestVoiceEnrollmentVerifyCannotExecuteAndDelete(t *testing.T) {
 verifier:=&fakeVoiceVerifier{match:true}
 id:=services.NewVoiceIdentityService(verifier)
 route:=voiceTestRouter(&VoiceRoutes{Identity:id,AuthEnabled:true})
 body:=new(bytes.Buffer)
 form:=multipart.NewWriter(body)
 if err:=form.WriteField("consent","yes");err!=nil{t.Fatal(err)}
 for _,field:=range []string{"sample1","sample2","sample3"}{
  part,err:=form.CreateFormFile(field,field+".wav")
  if err!=nil{t.Fatal(err)}
  if _,err:=part.Write(wavSample());err!=nil{t.Fatal(err)}
 }
 if err:=form.Close();err!=nil{t.Fatal(err)}
 req:=httptest.NewRequest("POST","/voice/enroll",body)
 req.Header.Set("Content-Type",form.FormDataContentType())
 w:=httptest.NewRecorder()
 route.ServeHTTP(w,req)
 if w.Code!=200{t.Fatalf("enrollment failed: %d %s",w.Code,w.Body.String())}

 req=httptest.NewRequest("POST","/voice/verify",bytes.NewReader(wavSample()))
 w=httptest.NewRecorder()
 route.ServeHTTP(w,req)
 if w.Code!=200{t.Fatalf("verification failed: %d %s",w.Code,w.Body.String())}
 var result struct {
  Verified bool `json:"verified"`
  CanExecute bool `json:"canExecute"`
 }
 if err:=json.Unmarshal(w.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if !result.Verified || result.CanExecute {t.Fatalf("verification must not authorize code execution: %+v",result)}

 verifier.match=false
 req=httptest.NewRequest("POST","/voice/verify",bytes.NewReader(wavSample()))
 w=httptest.NewRecorder()
 route.ServeHTTP(w,req)
 if err:=json.Unmarshal(w.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if result.Verified || result.CanExecute{t.Fatalf("mismatch must be rejected: %+v",result)}

 req=httptest.NewRequest("DELETE","/voice/profile",nil)
 w=httptest.NewRecorder()
 route.ServeHTTP(w,req)
 if w.Code!=200 || id.Enabled() || id.Enrolled(){t.Fatalf("profile was not deleted: %d",w.Code)}
}
func TestVoiceEnrollmentRejectsWithoutConsent(t *testing.T){
 id:=services.NewVoiceIdentityService(&fakeVoiceVerifier{})
 route:=voiceTestRouter(&VoiceRoutes{Identity:id,AuthEnabled:true})
 body:=new(bytes.Buffer)
 form:=multipart.NewWriter(body)
 for _,field:=range []string{"sample1","sample2","sample3"}{
  part,err:=form.CreateFormFile(field,field+".wav")
  if err!=nil{t.Fatal(err)}
  part.Write(wavSample())
 }
 form.Close()
 req:=httptest.NewRequest("POST","/voice/enroll",body)
 req.Header.Set("Content-Type",form.FormDataContentType())
 w:=httptest.NewRecorder()
 route.ServeHTTP(w,req)
 if w.Code!=400 || id.Enrolled(){t.Fatalf("enrollment without consent accepted: %d",w.Code)}
}
