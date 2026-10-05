package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestMCPServerService_AllowAllTools(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), auditRepo)

	create := func(name string, allowAll *bool, allowed, sideEffecting []string) (*domain.MCPServer, error) {
		return svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
			Name: name, Transport: "http", EndpointOrCommand: "https://mcp.example.com",
			AllowAllTools: allowAll, AllowedTools: allowed, SideEffectingTools: sideEffecting,
		})
	}
	update := func(srv *domain.MCPServer, allowAll *bool, allowed, sideEffecting []string) (*domain.MCPServer, error) {
		return svc.Update(t.Context(), tenantID, actorID, srv.ID, service.MCPServerSaveInput{
			Name: srv.Name, Transport: srv.Transport, EndpointOrCommand: srv.EndpointOrCommand,
			AllowAllTools: allowAll, AllowedTools: allowed, SideEffectingTools: sideEffecting,
		})
	}

	t.Run("defaults to an explicit allow-list when not asked for", func(t *testing.T) {
		srv, err := create("default", nil, nil, nil)
		require.NoError(t, err)
		assert.False(t, srv.AllowAllTools)
	})

	t.Run("allow-all can be set on create, with always-approve tools that are not allow-listed", func(t *testing.T) {
		srv, err := create("all", boolPtr(true), nil, []string{"isolate_host"})
		require.NoError(t, err)
		assert.True(t, srv.AllowAllTools)
		assert.Equal(t, []string{"isolate_host"}, srv.SideEffectingTools)
	})

	t.Run("allow-list mode still requires side-effecting tools to be allow-listed", func(t *testing.T) {
		_, err := create("strict", boolPtr(false), []string{"lookup_ip"}, []string{"isolate_host"})
		assert.ErrorContains(t, err, "must also be in allowed_tools")
	})

	t.Run("an update that does not mention allowAllTools leaves it alone (the Discover Tools save)", func(t *testing.T) {
		srv, err := create("keeps", boolPtr(true), nil, nil)
		require.NoError(t, err)
		got, err := update(srv, nil, []string{"lookup_ip"}, nil)
		require.NoError(t, err)
		assert.True(t, got.AllowAllTools, "an omitted flag must not silently flip the mode back")
	})

	t.Run("an explicit false switches back to the allow-list, and validation follows the new mode", func(t *testing.T) {
		srv, err := create("flips", boolPtr(true), nil, []string{"isolate_host"})
		require.NoError(t, err)
		_, err = update(srv, boolPtr(false), []string{"lookup_ip"}, []string{"isolate_host"})
		assert.ErrorContains(t, err, "must also be in allowed_tools")
		got, err := update(srv, boolPtr(false), []string{"lookup_ip", "isolate_host"}, []string{"isolate_host"})
		require.NoError(t, err)
		assert.False(t, got.AllowAllTools)
	})

	t.Run("update validates against the mode that will be saved, not the old one", func(t *testing.T) {
		srv, err := create("flip-on", boolPtr(false), []string{"lookup_ip"}, nil)
		require.NoError(t, err)
		got, err := update(srv, boolPtr(true), nil, []string{"isolate_host"})
		require.NoError(t, err)
		assert.True(t, got.AllowAllTools)
	})

	t.Run("the audit log records the mode", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 50)
		require.NoError(t, err)
		var all string
		for _, e := range events {
			all += string(e.Data)
		}
		assert.Contains(t, all, `"allowAllTools": true`)
	})
}

// toolServer is an MCP server whose tools/list carries annotations and which
// counts tools/call requests per tool.
type toolServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls map[string]int
}

func newToolServer(t *testing.T, toolsJSON string) *toolServer {
	t.Helper()
	ts := &toolServer{calls: map[string]int{}}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(result string) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(req.ID, 10) + `,"result":` + result + `}`))
		}
		switch req.Method {
		case "initialize":
			reply(`{"protocolVersion":"2025-03-26"}`)
		case "tools/list":
			reply(`{"tools":` + toolsJSON + `}`)
		case "tools/call":
			ts.mu.Lock()
			ts.calls[req.Params.Name]++
			ts.mu.Unlock()
			reply(`{"content":[{"type":"text","text":"ok"}],"isError":false}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *toolServer) callCount(name string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.calls[name]
}

func TestMCPToolService_ProposeToolCall_AllowAll(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()
	servers := repository.NewMCPServerRepository()
	mcpSvc := service.NewMCPServerService(pool, servers, store, repository.NewAdminAuditEventRepository())
	toolSvc := service.NewMCPToolService(pool, servers, repository.NewAIToolCallRepository(), store)

	ts := newToolServer(t, `[
		{"name":"lookup_ip","annotations":{"readOnlyHint":true}},
		{"name":"audited_read","annotations":{"readOnlyHint":true}},
		{"name":"isolate_host"},
		{"name":"wipe","annotations":{"readOnlyHint":false,"destructiveHint":true}}
	]`)
	srv, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "allow-all", Transport: "http", EndpointOrCommand: ts.URL,
		AllowAllTools: boolPtr(true), SideEffectingTools: []string{"audited_read"},
	})
	require.NoError(t, err)
	propose := func(tool string) (*domain.AIToolCall, error) {
		return toolSvc.ProposeToolCall(t.Context(), tenantID, srv.ID, "alert", uuid.New(), tool, map[string]any{})
	}

	t.Run("a tool the server declares read-only executes immediately, with no allow-list entry", func(t *testing.T) {
		call, err := propose("lookup_ip")
		require.NoError(t, err)
		assert.Equal(t, domain.ToolCallExecuted, call.Status)
		assert.Equal(t, 1, ts.callCount("lookup_ip"))
	})

	for name, why := range map[string]string{
		"isolate_host": "no annotations: unknown means approval, not trust",
		"wipe":         "declared destructive",
		"audited_read": "read-only, but the admin listed it as always-needs-approval",
	} {
		t.Run(name+" stops for approval ("+why+")", func(t *testing.T) {
			call, err := propose(name)
			require.NoError(t, err)
			assert.Equal(t, domain.ToolCallProposed, call.Status)
			assert.Equal(t, 0, ts.callCount(name), "it must not have been sent to the server")
		})
	}

	t.Run("a tool the server does not expose is refused, not forwarded", func(t *testing.T) {
		_, err := propose("invented_by_the_model")
		assert.ErrorContains(t, err, "not exposed by mcp server")
		assert.Equal(t, 0, ts.callCount("invented_by_the_model"))
	})

	t.Run("a tool added to the server after configuration is offered, and needs approval", func(t *testing.T) {
		later := newToolServer(t, `[{"name":"brand_new_tool"}]`)
		s2, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
			Name: "grows", Transport: "http", EndpointOrCommand: later.URL, AllowAllTools: boolPtr(true),
		})
		require.NoError(t, err)
		call, err := toolSvc.ProposeToolCall(t.Context(), tenantID, s2.ID, "alert", uuid.New(), "brand_new_tool", nil)
		require.NoError(t, err)
		assert.Equal(t, domain.ToolCallProposed, call.Status)
		assert.Equal(t, 0, later.callCount("brand_new_tool"))
	})

	t.Run("a disabled allow-all server refuses", func(t *testing.T) {
		require.NoError(t, mcpSvc.SetEnabled(t.Context(), tenantID, actorID, srv.ID, false))
		_, err := propose("lookup_ip")
		assert.ErrorContains(t, err, "disabled")
	})
}

func TestMCPToolService_AllowListServerUnchangedByAnnotations(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()
	servers := repository.NewMCPServerRepository()
	mcpSvc := service.NewMCPServerService(pool, servers, store, repository.NewAdminAuditEventRepository())
	toolSvc := service.NewMCPToolService(pool, servers, repository.NewAIToolCallRepository(), store)

	ts := newToolServer(t, `[{"name":"lookup_ip","annotations":{"readOnlyHint":true}},{"name":"other","annotations":{"readOnlyHint":true}}]`)
	srv, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "strict", Transport: "http", EndpointOrCommand: ts.URL, AllowedTools: []string{"lookup_ip"},
	})
	require.NoError(t, err)

	_, err = toolSvc.ProposeToolCall(t.Context(), tenantID, srv.ID, "alert", uuid.New(), "other", nil)
	assert.ErrorContains(t, err, "not in the allow-list", "a read-only hint must not widen an explicit allow-list")
	assert.Equal(t, 0, ts.callCount("other"))
}
