package handlers

import (
 "net/http"
 "testing"
)

func TestVoiceDialogueRejectsRemoteEndpoints(t *testing.T) {
 for _,tc:=range []struct{endpoint,model string}{
  {"https://127.0.0.1:11434","qwen"},
  {"http://example.com:11434","qwen"},
  {"http://192.168.0.4:11434","qwen"},
  {"http://127.0.0.1:11434/path","qwen"},
  {"http://127.0.0.1:11434?x=y","qwen"},
  {"http://127.0.0.1:11434",""},
 } {
  if _,err:=NewVoiceDialogue(nil,tc.endpoint,tc.model);err==nil{
   t.Fatalf("accepted unsafe configuration: %#v",tc)
  }
 }
}
func TestVoiceDialogueHasNoToolExecutionRoutes(t *testing.T){
 dialog,err:=NewVoiceDialogue(nil,"http://127.0.0.1:11434","local-test")
 if err!=nil{t.Fatal(err)}
 if dialog.Client == nil || dialog.Client.CheckRedirect == nil{
  t.Fatal("voice client must refuse redirects")
 }
 if got:=dialog.Client.CheckRedirect(&http.Request{},nil);got!=http.ErrUseLastResponse{
  t.Fatalf("redirect was not blocked: %v",got)
 }
}
