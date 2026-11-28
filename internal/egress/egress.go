// Package egress 经出口在线查询公网 IP 与所在的国家和时区，推出出口所在地的语言
package egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
)

// Exit 是一个出口的公网 IP 与按 IP 定位的国家、时区和语言
type Exit struct {
	IP       string `json:"ip"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

// lookupURLs 是返回请求方公网 IP、国家代码与 IANA 时区的查询服务，同时请求
var lookupURLs = []string{
	"https://ipinfo.io/json",
	"https://ipapi.co/json/",
	"https://ipwho.is/",
	"https://api.ip.sb/geoip",
}

// lookupTimeout 是一次出口查询等待全部查询服务的总时限
const lookupTimeout = 5 * time.Second

// lookupCacheTTL 是出口地理结果在内存中复用的有效期，避免同代理重复请求 GeoIP 服务
const lookupCacheTTL = 6 * time.Hour

// fallbackLocale 与 fallbackTimezone 是出口查询失败时使用的中性默认值，避免泄漏宿主机本地环境
const (
	fallbackLocale   = "en-US"
	fallbackTimezone = "UTC"
)

type exitCacheEntry struct {
	mu        sync.Mutex
	exit      Exit
	expiresAt time.Time
}

var (
	exitCacheMu sync.Mutex
	exitCache   = make(map[string]*exitCacheEntry)
)

func cacheEntryFor(proxy string) *exitCacheEntry {
	key := strings.TrimSpace(proxy)
	exitCacheMu.Lock()
	defer exitCacheMu.Unlock()
	entry := exitCache[key]
	if entry == nil {
		entry = &exitCacheEntry{}
		exitCache[key] = entry
	}
	return entry
}

// answer 是查询服务的响应：ipinfo.io 的国家代码在 country，其余在 country_code；ipwho.is 的时区是带 id 的对象，其余是字符串
type answer struct {
	IP          string          `json:"ip"`
	Country     string          `json:"country"`
	CountryCode string          `json:"country_code"`
	Timezone    json.RawMessage `json:"timezone"`
}

// Lookup 经 proxy 同时请求各查询服务，返回第一个给出有效 IP、国家代码与时区的结果，语言取该国 CLDR 最可能的语言；proxy 为空时直连，总时限 lookupTimeout
func Lookup(ctx context.Context, proxy string) (Exit, error) {
	exit, err := queryExit(ctx, proxy)
	if err == nil {
		entry := cacheEntryFor(proxy)
		entry.mu.Lock()
		entry.exit = exit
		entry.expiresAt = time.Now().Add(lookupCacheTTL)
		entry.mu.Unlock()
	}
	return exit, err
}

func cachedLookup(ctx context.Context, proxy string) (Exit, error) {
	entry := cacheEntryFor(proxy)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Now().Before(entry.expiresAt) {
		return entry.exit, nil
	}
	exit, err := queryExit(ctx, proxy)
	if err != nil {
		return Exit{}, err
	}
	entry.exit = exit
	entry.expiresAt = time.Now().Add(lookupCacheTTL)
	return exit, nil
}

func queryExit(ctx context.Context, proxy string) (Exit, error) {
	client, err := newClient(proxy)
	if err != nil {
		return Exit{}, err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	type result struct {
		exit Exit
		err  error
	}
	results := make(chan result, len(lookupURLs))
	for _, address := range lookupURLs {
		go func() {
			exit, err := query(ctx, client, address)
			results <- result{exit, err}
		}()
	}
	errs := make([]error, 0, len(lookupURLs))
	for range lookupURLs {
		result := <-results
		if result.err == nil {
			return result.exit, nil
		}
		errs = append(errs, result.err)
	}
	return Exit{}, errors.Join(errs...)
}

// Environment 返回账户的语言与时区：两项都给出时原样返回，否则经 proxy 查询出口（同代理复用缓存），未给出的项取出口所在地的语言与时区；查询失败时使用中性默认值 en-US / UTC，同时返回查询失败的原因
func Environment(ctx context.Context, proxy string, locale string, timezone string) (Exit, error) {
	if locale != "" && timezone != "" {
		return Exit{Locale: locale, Timezone: timezone}, nil
	}
	exit, err := cachedLookup(ctx, proxy)
	if err != nil {
		exit = Exit{Locale: fallbackLocale, Timezone: fallbackTimezone}
	}
	if locale != "" {
		exit.Locale = locale
	}
	if timezone != "" {
		exit.Timezone = timezone
	}
	return exit, err
}

// newClient 返回经 proxy 访问外网的 HTTP 客户端，proxy 为空时直连
func newClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("解析出口代理: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport}, nil
}

// query 请求一个查询服务并解析出 IP、国家与时区，按国家取 CLDR 最可能的语言
func query(ctx context.Context, client *http.Client, address string) (Exit, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Exit{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Exit{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Exit{}, fmt.Errorf("%s: %w", address, err)
	}
	if response.StatusCode != http.StatusOK {
		return Exit{}, fmt.Errorf("%s 返回 HTTP %d", address, response.StatusCode)
	}
	exit, err := parse(body)
	if err != nil {
		return Exit{}, fmt.Errorf("%s: %w", address, err)
	}
	return exit, nil
}

// parse 从查询服务的响应中取出 IP、国家代码与时区，三项都有效时返回出口
func parse(body []byte) (Exit, error) {
	var value answer
	if err := json.Unmarshal(body, &value); err != nil {
		return Exit{}, fmt.Errorf("响应不是 JSON: %w", err)
	}
	address, err := netip.ParseAddr(strings.TrimSpace(value.IP))
	if err != nil {
		return Exit{}, fmt.Errorf("响应中的 IP 无效: %q", value.IP)
	}
	code := value.CountryCode
	if code == "" {
		code = value.Country
	}
	region, err := language.ParseRegion(strings.TrimSpace(code))
	if err != nil || !region.IsCountry() {
		return Exit{}, fmt.Errorf("响应中的国家代码无效: %q", code)
	}
	var zone string
	if json.Unmarshal(value.Timezone, &zone) != nil {
		var object struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(value.Timezone, &object) == nil {
			zone = object.ID
		}
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
		return Exit{}, fmt.Errorf("响应中的时区无效: %q", zone)
	}
	likely, _ := language.Compose(language.Und, region)
	base, _ := likely.Base()
	locale, _ := language.Compose(base, region)
	return Exit{IP: address.Unmap().String(), Country: region.String(), Timezone: zone, Locale: locale.String()}, nil
}
