package aistudio

import (
	"math"
	"math/rand/v2"
	"time"
)

// pageLoad 是一次以浏览器导航形状请求官网页面的实测结果
type pageLoad struct {
	url      string
	protocol string
	// started 是导航开始的时刻
	started time.Time
	// connection 是文档请求建立连接的实测时刻，复用连接时 dialed 为 false
	connection *connectionTiming
	// headersAt 是收到文档响应头的时刻
	headersAt time.Time
	// end 是读完文档正文时距导航开始的时长
	end time.Duration
	// encoded 是响应头给出的压缩正文字节数，未知或未提供时为 0
	encoded int
	decoded int
}

// navigationTiming 按一次页面载入的实测时刻生成 Firefox 158 的 PerformanceNavigationTiming 网络阶段
// 各时刻为以导航开始为零点的毫秒数，第二个返回值为 performance.timing 的 domLoading
func navigationTiming(load pageLoad) (map[string]any, int) {
	at := func(moment time.Time) int { return int(moment.Sub(load.started).Milliseconds()) }
	fetch := rand.IntN(2)
	lookup, lookupEnd, connect, secure, connectEnd := fetch, fetch, fetch, fetch, fetch
	request := fetch + 2 + rand.IntN(12)
	if timing := load.connection; timing != nil && timing.dialed {
		lookup = fetch + rand.IntN(14)
		lookupEnd = lookup
		if !timing.dnsStart.IsZero() && !timing.dnsDone.IsZero() {
			lookupEnd += max(0, int(timing.dnsDone.Sub(timing.dnsStart).Milliseconds()))
		}
		connect = lookupEnd + rand.IntN(2)
		connectEnd = connect
		if !timing.connectStart.IsZero() && !timing.connectDone.IsZero() {
			connectEnd += max(0, int(timing.connectDone.Sub(timing.connectStart).Milliseconds()))
		}
		if !timing.tlsStart.IsZero() && !timing.tlsDone.IsZero() {
			if !timing.connectStart.IsZero() {
				secure = max(connect, connect+int(timing.tlsStart.Sub(timing.connectStart).Milliseconds()))
			} else {
				secure = max(connectEnd, at(timing.tlsStart))
			}
			tlsDuration := max(1, int(timing.tlsDone.Sub(timing.tlsStart).Milliseconds()))
			connectEnd = max(connectEnd, secure+tlsDuration)
		} else {
			secure = connectEnd
		}
		request = connectEnd + 1 + rand.IntN(4)
	}
	endMs := int(load.end.Milliseconds())
	headersMs := endMs
	if !load.headersAt.IsZero() {
		headersMs = at(load.headersAt)
	}
	responseStart := max(request+1, headersMs)
	if timing := load.connection; timing != nil && timing.dialed && !timing.tunneled.IsZero() && timing.tlsStart.IsZero() {
		responseStart = max(responseStart, connectEnd+at(timing.tunneled)-at(timing.connectDone))
	}
	headers := responseStart
	responseEnd := max(responseStart, endMs)
	if responseEnd <= responseStart {
		responseEnd = responseStart + max(1, load.decoded/(64*1024)+1+rand.IntN(3))
	}
	encoded := load.encoded
	if encoded <= 0 {
		ratio := 0.175 + 0.04*rand.Float64()
		encoded = max(0, int(math.Round(float64(load.decoded)*ratio))+rand.IntN(13)-6)
	}
	transfer := encoded + 220 + rand.IntN(160)
	entry := map[string]any{
		"name": load.url, "startTime": 0, "nextHopProtocol": load.protocol,
		"fetchStart": fetch, "domainLookupStart": lookup, "domainLookupEnd": lookupEnd,
		"connectStart": connect, "connectEnd": connectEnd, "secureConnectionStart": secure,
		"requestStart": request, "finalResponseHeadersStart": headers, "responseStart": responseStart, "responseEnd": responseEnd,
		"transferSize": transfer, "encodedBodySize": encoded, "decodedBodySize": load.decoded,
	}
	return entry, responseStart + pick(0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 2, 3)
}

// documentTiming 补全导航条目的文档解析与 load 阶段：domInteractive 不早于读完正文与文档子资源实测载入完成（resources，距导航开始）的较晚者，其后执行脚本与页面应用的时长按 Firefox 158 现场分布抽样
func documentTiming(entry map[string]any, resources time.Duration) {
	ready := max(entry["responseEnd"].(int), int(resources.Milliseconds()))
	interactive := ready + max(200, lognormal(6.13, 0.62))
	dclStart := interactive + 1 + rand.IntN(6)
	dclEnd := dclStart + pick(0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 4)
	complete := max(dclEnd, interactive+lognormal(6.96, 0.48))
	loadStart := complete + rand.IntN(4)
	loadEnd := loadStart + pick(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1)
	entry["domInteractive"] = interactive
	entry["domContentLoadedEventStart"] = dclStart
	entry["domContentLoadedEventEnd"] = dclEnd
	entry["domComplete"] = complete
	entry["loadEventStart"] = loadStart
	entry["loadEventEnd"] = loadEnd
	entry["duration"] = loadEnd
}

// nextHopProtocol 返回响应协议对应的 ALPN 名称
func nextHopProtocol(proto string) string {
	switch proto {
	case "HTTP/2.0":
		return "h2"
	default:
		return "http/1.1"
	}
}

// lognormal 返回对数均值 mu、对数标准差 sigma 的对数正态分布取整样本
func lognormal(mu, sigma float64) int {
	return int(math.Round(math.Exp(mu + sigma*rand.NormFloat64())))
}

// pick 返回等概率取出的一个现场值
func pick(values ...int) int {
	return values[rand.IntN(len(values))]
}
