package identity
import("bytes";"context";"strings";"testing";"github.com/alpyxn/varyaone/internal/platform/database";"github.com/jackc/pgx/v5";"github.com/jackc/pgx/v5/pgconn")
type reviewDB struct{database.Querier;pending []byte;replaced bool}
func(d *reviewDB)Exec(_ context.Context,q string,a ...any)(pgconn.CommandTag,error){if strings.Contains(q,"SET totp_pending_ciphertext="){d.pending=a[0].([]byte)};if strings.Contains(q,"SET totp_secret_ciphertext=totp_pending_ciphertext"){d.replaced=true};return pgconn.NewCommandTag("UPDATE 1"),nil}
func(d *reviewDB)Begin(context.Context)(pgx.Tx,error){return &reviewTx{d:d},nil}
type reviewTx struct{pgx.Tx;d *reviewDB}
func(t *reviewTx)Exec(c context.Context,q string,a ...any)(pgconn.CommandTag,error){return t.d.Exec(c,q,a...)}
func(t *reviewTx)QueryRow(context.Context,string,...any)pgx.Row{return reviewRow{t.d.pending}}
func(t *reviewTx)Commit(context.Context)error{return nil}
func(t *reviewTx)Rollback(context.Context)error{return nil}
type reviewRow struct{b []byte}
func(r reviewRow)Scan(a ...any)error{*a[0].(*[]byte)=r.b;return nil}
func TestSecurityReviewReplaceEnabledTOTP(t *testing.T){
 d:=&reviewDB{};s,e:=NewService(d,bytes.Repeat([]byte{1},32));if e!=nil{t.Fatal(e)}
 session:=Session{User:User{ID:"11111111-1111-4111-8111-111111111111",Email:"review@example.test",TOTPEnabled:true},CurrentCompanyID:"22222222-2222-4222-8222-222222222222"}
 secret,_,e:=s.BeginTOTP(context.Background(),session,RequestMeta{});if e!=nil{t.Fatal(e)}
 codes,e:=s.ConfirmTOTP(context.Background(),session,generateTOTP(secret,uint64(s.now().Unix()/30)),RequestMeta{});if e!=nil{t.Fatal(e)}
 if !d.replaced||len(codes)!=8{t.Fatal("replacement did not reach database")}
 t.Log("Enabled TOTP replacement reaches UPDATE and returns recovery codes without password or old TOTP")
}
