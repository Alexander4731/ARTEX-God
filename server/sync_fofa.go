package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// 资产同步（FOFA 数据源）。
//
// FOFA 是网络空间测绘搜索引擎。与 ScopeSentry（MCP）不同，FOFA 是直连的 REST 搜索
// API：operator 在「资产同步」页填 email + API key，用 FOFA 语法检索，预览后把结果
// 归并写入资产图（可选关联企业）。凭据存 settings 表（fofa.email / fofa.key），key 的
// 回显/保存沿用 notify 的掩码约定（永不回显明文、留空/掩码表示保留原值）。
//
// 纯新增、operator 手动驱动；不改动 ScopeSentry、资产 Upsert 逻辑或任何表结构。
// FOFA 调用走系统全局代理（GlobalProxy），不经 MITM 录制代理——它不是渗透目标。
const (
	fofaAPIBase       = "https://fofa.info"
	fofaFields        = "host,ip,port,protocol,domain,title,server,icp" // 固定字段，按下标解析
	fofaMaxResults    = 5000                                            // 单次 sync 入库保护上限
	fofaPreviewMax    = 100                                             // 预览单页上限
	fofaDefaultPage   = 100                                             // 默认分页大小
	settingFofaEmail  = "fofa.email"
	settingFofaKey    = "fofa.key"
	fofaClientTimeout = 25 * time.Second
)

// fofaRow 是预览返回给前端的一行（字段子集）。
type fofaRow struct {
	Host     string `json:"host"`
	IP       string `json:"ip"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
	Domain   string `json:"domain"`
	Title    string `json:"title"`
	Server   string `json:"server"`
}

// fofaSearchResp 是 FOFA search/all 的响应。results 为字符串二维数组（fields 多字段时）。
type fofaSearchResp struct {
	Error   bool       `json:"error"`
	Errmsg  string     `json:"errmsg"`
	Mode    string     `json:"mode"`
	Size    int        `json:"size"`
	Results [][]string `json:"results"`
}

// fofaCreds reads the stored FOFA email + key from settings.
func (s *Server) fofaCreds() (email, key string) {
	email, _, _ = s.m.pg.GetSetting(settingFofaEmail)
	key, _, _ = s.m.pg.GetSetting(settingFofaKey)
	return strings.TrimSpace(email), strings.TrimSpace(key)
}

// fofaClient builds an outbound client that honors the configured global egress proxy
// (empty = direct). Mirrors enrich.buildClient's per-request proxy closure; keeps TLS
// verification on (fofa.info has a valid cert).
func (s *Server) fofaClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeout,
		Proxy: func(*http.Request) (*url.URL, error) {
			p := s.m.GlobalProxy()
			if strings.TrimSpace(p) == "" {
				return nil, nil // direct
			}
			return url.Parse(p)
		},
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// fofaGet issues a GET to a FOFA API path with email/key + extra params and decodes JSON.
func (s *Server) fofaGet(ctx context.Context, email, key, path string, extra url.Values, out any) error {
	q := url.Values{}
	if email != "" {
		q.Set("email", email)
	}
	q.Set("key", key)
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	u := fofaAPIBase + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.fofaClient(fofaClientTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("FOFA 认证失败（HTTP %d），请检查 email / key", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("FOFA 返回 HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// fofaSearch runs one page of search/all. size is the page size (<= page cap), page is 1-based.
func (s *Server) fofaSearch(ctx context.Context, email, key, query string, size, page int) (fofaSearchResp, error) {
	var out fofaSearchResp
	extra := url.Values{}
	extra.Set("qbase64", base64.StdEncoding.EncodeToString([]byte(query)))
	extra.Set("fields", fofaFields)
	extra.Set("size", strconv.Itoa(size))
	extra.Set("page", strconv.Itoa(page))
	if err := s.fofaGet(ctx, email, key, "/api/v1/search/all", extra, &out); err != nil {
		return out, err
	}
	if out.Error {
		msg := out.Errmsg
		if msg == "" {
			msg = "FOFA 查询失败"
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

// fofaProbe validates the account via /api/v1/info/my.
func (s *Server) fofaProbe(ctx context.Context, email, key string) error {
	var out struct {
		Error  bool   `json:"error"`
		Errmsg string `json:"errmsg"`
	}
	if err := s.fofaGet(ctx, email, key, "/api/v1/info/my", url.Values{}, &out); err != nil {
		return err
	}
	if out.Error {
		if out.Errmsg == "" {
			out.Errmsg = "账号校验失败"
		}
		return fmt.Errorf("%s", out.Errmsg)
	}
	return nil
}

// rowToFofaRow maps a results row (ordered by fofaFields) into the preview struct.
func rowToFofaRow(row []string) fofaRow {
	at := func(i int) string {
		if i < len(row) {
			return row[i]
		}
		return ""
	}
	return fofaRow{
		Host: at(0), IP: at(1), Port: at(2), Protocol: at(3),
		Domain: at(4), Title: at(5), Server: at(6),
	}
}

// ---------- GET /api/sync/fofa/status ----------
func (s *Server) syncFofaStatus(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	email, key := s.fofaCreds()
	configured := key != ""
	reachable := false
	if configured {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		reachable = s.fofaProbe(ctx, email, key) == nil
	}
	writeJSON(w, 200, map[string]any{
		"configured": configured,
		"reachable":  reachable,
		"email":      email,
	})
}

// ---------- POST /api/sync/fofa/datasource ----------
func (s *Server) syncFofaDatasource(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	var body struct {
		Email string `json:"email"`
		Key   string `json:"key"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	curEmail, curKey := s.fofaCreds()

	// email 非密：空=保留原值，非空=覆盖。
	newEmail := curEmail
	if e := strings.TrimSpace(body.Email); e != "" {
		newEmail = e
	}
	// key：空 或 掩码 = 保留原值（沿用 notify 掩码约定）；其它=覆盖。
	newKey := curKey
	if k := strings.TrimSpace(body.Key); k != "" && !notify.IsMasked(k) {
		newKey = k
	}

	if newEmail != curEmail {
		if err := s.m.pg.SetSetting(settingFofaEmail, newEmail); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if newKey != curKey {
		if err := s.m.pg.SetSetting(settingFofaKey, newKey); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"configured": newKey != "",
		"email":      newEmail,
	})
}

// ---------- POST /api/sync/fofa/query ---------- (预览，不写库)
func (s *Server) syncFofaQuery(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	var body struct {
		Query string `json:"query"`
		Size  int    `json:"size"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	query := strings.TrimSpace(body.Query)
	if query == "" {
		writeErr(w, 400, "query 不能为空")
		return
	}
	email, key := s.fofaCreds()
	if key == "" {
		writeErr(w, 400, "未配置 FOFA key，请先在上方保存账号")
		return
	}
	size := body.Size
	if size <= 0 || size > fofaPreviewMax {
		size = fofaPreviewMax
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.fofaSearch(ctx, email, key, query, size, 1)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	rows := make([]fofaRow, 0, len(resp.Results))
	for _, row := range resp.Results {
		rows = append(rows, rowToFofaRow(row))
	}
	writeJSON(w, 200, map[string]any{
		"total": resp.Size,
		"mode":  resp.Mode,
		"rows":  rows,
	})
}

// ---------- POST /api/sync/fofa/sync ---------- (检索 + 导入资产)
func (s *Server) syncFofaRun(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	as := s.assetStore()
	cs := s.companyStore()
	if as == nil || cs == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	var req struct {
		Query         string `json:"query"`
		Size          int    `json:"size"`
		CreateCompany bool   `json:"create_company"`
		CompanyName   string `json:"company_name"`
		PageSize      int    `json:"page_size"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeErr(w, 400, "query 不能为空")
		return
	}
	email, key := s.fofaCreds()
	if key == "" {
		writeErr(w, 400, "未配置 FOFA key，请先在上方保存账号")
		return
	}
	if req.CreateCompany && strings.TrimSpace(req.CompanyName) == "" {
		writeErr(w, 400, "勾选了「创建企业」时必须填写企业名称")
		return
	}
	target := req.Size
	if target <= 0 || target > fofaMaxResults {
		target = fofaMaxResults
	}
	pageSize := req.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = fofaDefaultPage
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	// 先建公司（拿到 id）；scope 规则在导入后按实际发现的根域写入，再重算归属。
	var cid int64
	var companies []string
	if req.CreateCompany {
		name := strings.TrimSpace(req.CompanyName)
		id, err := cs.UpsertByName(name)
		if err != nil {
			writeErr(w, 500, "创建企业失败: "+err.Error())
			return
		}
		cid = id
		companies = append(companies, name)
	}

	synced := map[string]int{"service": 0, "ip": 0, "subdomain": 0, "domain": 0}
	var warnings, errs []string
	rootSet := map[string]struct{}{}
	imported := 0
	fofaTotal := 0
	truncated := false

	for page := 1; imported < target; page++ {
		resp, err := s.fofaSearch(ctx, email, key, query, pageSize, page)
		if err != nil {
			errs = append(errs, fmt.Sprintf("第 %d 页拉取失败: %v", page, err))
			break
		}
		if page == 1 {
			fofaTotal = resp.Size
		}
		if len(resp.Results) == 0 {
			break
		}
		for _, row := range resp.Results {
			if imported >= target {
				truncated = true
				break
			}
			root, e := fofaIngest(as, row, synced)
			if e != "" {
				errs = append(errs, e)
			}
			if root != "" {
				rootSet[root] = struct{}{}
			}
			imported++
		}
		if len(resp.Results) < pageSize {
			break // 最后一页
		}
		if imported >= target {
			truncated = fofaTotal > imported
			break
		}
	}

	// 把发现的根域写进公司 scope，再重算归属，让刚导入的资产挂到公司。
	if cid != 0 && len(rootSet) > 0 {
		roots := make([]string, 0, len(rootSet))
		for r := range rootSet {
			roots = append(roots, r)
		}
		cs.AddScope(cid, roots, "fofa:"+strings.TrimSpace(req.CompanyName))
		_ = cs.RecomputeAttribution()
	}

	writeJSON(w, 200, map[string]any{
		"synced":    synced,
		"total":     fofaTotal,
		"imported":  imported,
		"truncated": truncated,
		"companies": companies,
		"warnings":  warnings,
		"errors":    errs,
	})
}

// fofaIngest maps one FOFA result row (ordered by fofaFields) into asset-graph upserts.
// Returns the discovered root domain (for company scope) and a non-empty error string on
// a notable failure (collected, non-fatal). Company attribution happens via scope.
func fofaIngest(as *db.AssetStore, row []string, synced map[string]int) (root string, errStr string) {
	at := func(i int) string {
		if i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	host := at(0)
	ip := at(1)
	portStr := at(2)
	protocol := strings.ToLower(at(3))
	domain := at(4)
	title := at(5)

	scheme, hostname, hostPort := splitFofaHost(host)
	if portStr == "" {
		portStr = hostPort
	}

	// IP 资产。
	if net.ParseIP(ip) != nil {
		bound := domain
		if bound == "" {
			bound = hostname
		}
		boundList := []string(nil)
		if bound != "" {
			boundList = []string{bound}
		}
		if _, err := as.UpsertIP(db.UpsertIPReq{IP: ip, BoundDomains: boundList}); err == nil {
			synced["ip"]++
		} else {
			errStr = "IP " + ip + " 入库失败: " + err.Error()
		}
	}

	// 域名：根域 + （host 不同于根域时）子域。
	if domain != "" {
		if _, err := as.UpsertRootDomain(db.UpsertRootDomainReq{Domain: domain}); err == nil {
			synced["domain"]++
		}
		if r, _ := db.RootDomain(domain); r != "" {
			root = r
		} else {
			root = domain
		}
		if hostname != "" && hostname != domain && net.ParseIP(hostname) == nil {
			if _, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: hostname}); err == nil {
				synced["subdomain"]++
			}
		}
	}

	// 服务：HTTP(S) → HTTP 服务；否则 → 其它服务。
	isHTTP := protocol == "http" || protocol == "https" || scheme == "http" || scheme == "https"
	if isHTTP {
		u := buildFofaURL(scheme, protocol, hostname, hostPort)
		if u != "" {
			if _, err := as.UpsertHTTPService(db.UpsertHTTPServiceReq{URL: u, PageTitle: title, IP: ip}); err == nil {
				synced["service"]++
			} else {
				errStr = "HTTP 服务 " + u + " 入库失败: " + err.Error()
			}
		}
	} else if protocol != "" && portStr != "" {
		if port, perr := strconv.Atoi(portStr); perr == nil && port > 0 && port <= 65535 {
			d := domain
			if d == "" && net.ParseIP(hostname) == nil {
				d = hostname
			}
			if _, err := as.UpsertOtherService(db.UpsertOtherServiceReq{Domain: d, IP: ip, Port: port, ServiceName: protocol}); err == nil {
				synced["service"]++
			}
		}
	}
	return root, errStr
}

// splitFofaHost parses a FOFA host field into (scheme, hostname, port). Accepts forms like
// "https://example.com", "example.com:8080", "1.2.3.4:443", "example.com".
func splitFofaHost(host string) (scheme, hostname, port string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", ""
	}
	if strings.Contains(host, "://") {
		if u, err := url.Parse(host); err == nil {
			return strings.ToLower(u.Scheme), u.Hostname(), u.Port()
		}
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		return "", h, p
	}
	return "", host, ""
}

// buildFofaURL constructs a normalized http(s) URL from the parsed host parts.
func buildFofaURL(scheme, protocol, hostname, port string) string {
	if hostname == "" {
		return ""
	}
	sc := scheme
	if sc == "" {
		sc = protocol
	}
	if sc != "http" && sc != "https" {
		if port == "443" {
			sc = "https"
		} else {
			sc = "http"
		}
	}
	hostpart := hostname
	if port != "" {
		defaultPort := (sc == "http" && port == "80") || (sc == "https" && port == "443")
		if !defaultPort {
			hostpart = net.JoinHostPort(hostname, port)
		}
	}
	return sc + "://" + hostpart
}
