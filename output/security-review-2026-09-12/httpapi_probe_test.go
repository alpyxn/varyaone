package httpapi
import("net/http/httptest";"testing")
func TestSecurityReviewForgedForwardedIP(t *testing.T){
 r:=httptest.NewRequest("POST","http://example.test/api/v1/auth/login",nil)
 r.RemoteAddr="192.0.2.10:5555"
 for _,ip:=range []string{"198.51.100.1","198.51.100.2"}{r.Header.Set("X-Forwarded-For",ip);if got:=clientIP(r);got!=ip{t.Fatalf("got %s",got)}}
 t.Log("Same network peer controls the login limiter IP through X-Forwarded-For")
}
