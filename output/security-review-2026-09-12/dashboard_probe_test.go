package dashboard
import("context";"errors";"strings";"testing";"github.com/alpyxn/varyaone/internal/identity";"github.com/alpyxn/varyaone/internal/platform/database";"github.com/jackc/pgx/v5")
type reviewDB struct{database.Querier;q string;args []any}
func(d *reviewDB)Query(_ context.Context,q string,a ...any)(pgx.Rows,error){d.q=q;d.args=a;return nil,errors.New("query captured")}
func TestSecurityReviewFeedScope(t *testing.T){
 for _,perm:=range []string{"inventory.read","sales.quote.read"}{
 d:=&reviewDB{};s:=NewService(d);_,_=s.RecentActivity(context.Background(),identity.Session{User:identity.User{ID:"11111111-1111-4111-8111-111111111111"},CurrentCompanyID:"22222222-2222-4222-8222-222222222222",Permissions:[]string{perm}},15)
 if len(d.args)!=1||strings.Contains(d.q,"membership_"){t.Fatal("unexpected scope filter")}
 if perm=="sales.quote.read" && !strings.Contains(d.q,"WHERE d.company_id = $1 AND d.status = 'DRAFT'"){t.Fatal("unexpected document filtering")}
 t.Logf("%s: query has company only; no user scope or document-type predicate",perm)
 }
}
