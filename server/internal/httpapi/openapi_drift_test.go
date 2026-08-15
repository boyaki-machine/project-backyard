// docs/openapi.yaml と実装のルート一覧を突き合わせる（Design.md 3.3）。
//
// openapi.yaml は「実装済みAPIの現状」を名乗る（ApiDesign.md 1.3）。名乗りと
// 実態のずれは最悪のケースになるため、仕組みで捕まえる。スキーマの中身までは
// 見ないが、**パスとメソッドの欠落・余剰は確実に捕まる。**
package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// openAPIPath は本テストから見た openapi.yaml の位置。
// server/internal/httpapi → リポジトリルート。
const openAPIPath = "../../../docs/openapi.yaml"

// operation は「メソッド＋パス」の1件。表示のために文字列で持つ。
//
//	GET /api/v1/me
type operation string

// httpMethods は paths.<path> の下でオペレーションとして扱うキー。
// summary / parameters などの非オペレーションと区別する。
var httpMethods = []string{
	strings.ToLower(http.MethodGet),
	strings.ToLower(http.MethodHead),
	strings.ToLower(http.MethodPost),
	strings.ToLower(http.MethodPut),
	strings.ToLower(http.MethodPatch),
	strings.ToLower(http.MethodDelete),
	strings.ToLower(http.MethodOptions),
	"trace",
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	implemented := walkRoutes(t)
	documented := readOpenAPIOperations(t)

	for _, op := range diff(implemented, documented) {
		t.Errorf("%s が実装されているが docs/openapi.yaml に無い"+
			"（APIを追加したステップの成果物に openapi.yaml の更新を含めること）", op)
	}
	for _, op := range diff(documented, implemented) {
		t.Errorf("%s が docs/openapi.yaml にあるが実装されていない"+
			"（openapi.yaml は設計ではなく実装済みの現状を書く。ApiDesign.md 1.3）", op)
	}
}

// walkRoutes は実装のルート一覧を返す（chi.Walk）。
//
// Deps を空で渡す。ルートを並べるだけで、リクエストを1つも処理しないため
// DB もプールも要らない。
func walkRoutes(t *testing.T) map[operation]bool {
	t.Helper()

	r, ok := NewRouter(Deps{}).(chi.Routes)
	if !ok {
		t.Fatal("ルータが chi.Routes を満たさない。実装のルート一覧を取れない")
	}

	ops := map[operation]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi は末尾に / を付けた形も歩く（/api/v1/me/ など）。同じ経路なので畳む。
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			return nil
		}
		ops[operation(method+" "+route)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("ルートが1つも取れなかった。テストが常に通る状態になっている")
	}
	return ops
}

// readOpenAPIOperations は openapi.yaml の paths からオペレーション一覧を返す。
//
// yaml.v3 は dev seed（DbDesign.md 7.6.4）のために既に依存にある。
// 本テストのためにライブラリを増やしていない。
func readOpenAPIOperations(t *testing.T) map[operation]bool {
	t.Helper()

	b, err := os.ReadFile(filepath.Clean(openAPIPath))
	if err != nil {
		t.Fatalf("docs/openapi.yaml を読めない: %v", err)
	}

	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("docs/openapi.yaml が YAML として壊れている: %v", err)
	}

	ops := map[operation]bool{}
	for path, item := range doc.Paths {
		for key := range item {
			for _, m := range httpMethods {
				if strings.EqualFold(key, m) {
					ops[operation(strings.ToUpper(key)+" "+path)] = true
				}
			}
		}
	}
	if len(ops) == 0 {
		t.Fatal("docs/openapi.yaml に paths が1つも無い")
	}
	return ops
}

// diff は a にあって b に無いものを並べる（表示のため昇順）。
func diff(a, b map[operation]bool) []operation {
	var only []operation
	for op := range a {
		if !b[op] {
			only = append(only, op)
		}
	}
	sort.Slice(only, func(i, j int) bool { return only[i] < only[j] })
	return only
}
