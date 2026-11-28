package camoufoxnative

import (
	"maps"
	"math"
	"math/rand/v2"
	"strings"
)

// weighted 是带出现权重的取值
type weighted[T any] struct {
	value  T
	weight float64
}

// draw 按权重抽取一个取值
func draw[T any](items []weighted[T]) T {
	total := 0.0
	for _, item := range items {
		total += item.weight
	}
	point := rand.Float64() * total
	for _, item := range items {
		if point < item.weight {
			return item.value
		}
		point -= item.weight
	}
	return items[len(items)-1].value
}

// windowsDisplay 是一块 Windows 主屏的物理分辨率与按 Windows 默认缩放规则可能的缩放比例
type windowsDisplay struct {
	width, height int
	scales        []weighted[float64]
}

// hardwareTier 表示新账号指纹抽取时的 Windows 机器硬件档位
type hardwareTier int

const (
	hardwareTierLow hardwareTier = iota
	hardwareTierMainstream
	hardwareTierHigh
)

// hardwareProfile 是一组档位一致的 Windows 硬件、显示、音频与存储配额抽样结果
type hardwareProfile struct {
	tier         hardwareTier
	concurrency  int
	display      windowsDisplay
	scale        float64
	taskbar      int
	webgl        map[string]any
	audioRate    int
	audioChans   int
	storageLimit int
}

// windowsHardwareTiers 是新账号指纹中低配、主流、高配 Windows 机器的抽样权重
var windowsHardwareTiers = []weighted[hardwareTier]{
	{hardwareTierLow, 0.25},
	{hardwareTierMainstream, 0.55},
	{hardwareTierHigh, 0.20},
}

var (
	display1920x1080 = windowsDisplay{1920, 1080, []weighted[float64]{{1, 0.5}, {1.25, 0.35}, {1.5, 0.15}}}
	display1366x768  = windowsDisplay{1366, 768, []weighted[float64]{{1, 1}}}
	display1920x1200 = windowsDisplay{1920, 1200, []weighted[float64]{{1, 0.4}, {1.25, 0.3}, {1.5, 0.3}}}
	display2560x1440 = windowsDisplay{2560, 1440, []weighted[float64]{{1, 0.45}, {1.25, 0.3}, {1.5, 0.25}}}
	display1600x900  = windowsDisplay{1600, 900, []weighted[float64]{{1, 0.85}, {1.25, 0.15}}}
	display3840x2160 = windowsDisplay{3840, 2160, []weighted[float64]{{1.5, 0.55}, {1.25, 0.1}, {1.75, 0.1}, {2, 0.15}, {2.5, 0.1}}}
	display1440x900  = windowsDisplay{1440, 900, []weighted[float64]{{1, 1}}}
	display1280x1024 = windowsDisplay{1280, 1024, []weighted[float64]{{1, 1}}}
	display1680x1050 = windowsDisplay{1680, 1050, []weighted[float64]{{1, 1}}}
	display1024x768  = windowsDisplay{1024, 768, []weighted[float64]{{1, 1}}}
	display3440x1440 = windowsDisplay{3440, 1440, []weighted[float64]{{1, 1}}}
)

// tierDisplays 是各硬件档位对应的主屏分辨率与缩放分布
var tierDisplays = map[hardwareTier][]weighted[windowsDisplay]{
	hardwareTierLow: {
		{display1920x1080, 0.35},
		{display1366x768, 0.35},
		{display1600x900, 0.12},
		{display1440x900, 0.06},
		{display1280x1024, 0.05},
		{display1680x1050, 0.04},
		{display1024x768, 0.03},
	},
	hardwareTierMainstream: {
		{display1920x1080, 0.65},
		{display1920x1200, 0.12},
		{display2560x1440, 0.10},
		{display1366x768, 0.08},
		{display1600x900, 0.05},
	},
	hardwareTierHigh: {
		{display1920x1080, 0.35},
		{display2560x1440, 0.30},
		{display3840x2160, 0.15},
		{display1920x1200, 0.12},
		{display3440x1440, 0.08},
	},
}

const (
	// windowsTaskbarWin10 是 Windows 10 默认任务栏高度（DIP）
	windowsTaskbarWin10 = 40
	// windowsTaskbarWin11 是 Windows 11 默认任务栏高度（DIP）
	windowsTaskbarWin11 = 48
	// firefoxTaskbar 是缺失或异常 availHeight 时回退使用的默认任务栏高度（DIP）
	firefoxTaskbar = windowsTaskbarWin11
)

// tierTaskbars 是各硬件档位对应的 Windows 10 / Windows 11 任务栏高度占比
var tierTaskbars = map[hardwareTier][]weighted[int]{
	hardwareTierLow:        {{windowsTaskbarWin10, 0.55}, {windowsTaskbarWin11, 0.45}},
	hardwareTierMainstream: {{windowsTaskbarWin11, 0.65}, {windowsTaskbarWin10, 0.35}},
	hardwareTierHigh:       {{windowsTaskbarWin11, 0.80}, {windowsTaskbarWin10, 0.20}},
}

// tierThreads 是各硬件档位对应的逻辑处理器数（navigator.hardwareConcurrency）占比
var tierThreads = map[hardwareTier][]weighted[int]{
	hardwareTierLow: {
		{2, 0.20}, {4, 0.45}, {6, 0.15}, {8, 0.20},
	},
	hardwareTierMainstream: {
		{4, 0.10}, {6, 0.12}, {8, 0.38}, {12, 0.25}, {16, 0.15},
	},
	hardwareTierHigh: {
		{12, 0.15}, {16, 0.35}, {20, 0.15}, {22, 0.05}, {24, 0.15}, {32, 0.15},
	},
}

// firefoxGPUVendors 是 Firefox 硬件报告中 Windows 可能的显卡厂商占比，Microsoft 为没有显卡驱动时的基本显示驱动
var firefoxGPUVendors = map[string]float64{"Intel": 0.6381, "AMD": 0.1422, "NVIDIA": 0.1308, "Microsoft": 0.0122}

// tierGPUVendors 是各硬件档位对应的显卡厂商权重，中高配不包含 Microsoft 基本显示驱动
var tierGPUVendors = map[hardwareTier][]weighted[string]{
	hardwareTierLow: {
		{"Intel", 0.72}, {"AMD", 0.15}, {"NVIDIA", 0.08}, {"Microsoft", 0.05},
	},
	hardwareTierMainstream: {
		{"Intel", 0.60}, {"NVIDIA", 0.22}, {"AMD", 0.18},
	},
	hardwareTierHigh: {
		{"NVIDIA", 0.55}, {"AMD", 0.25}, {"Intel", 0.20},
	},
}

// windowsAudioSampleRates 是 Windows 桌面输出设备常见的 AudioContext 采样率占比
var windowsAudioSampleRates = []weighted[int]{
	{48000, 0.80},
	{44100, 0.20},
}

// tierStorageLimits 是各硬件档位对应的离散化临时存储配额上限（KB）
var tierStorageLimits = map[hardwareTier][]weighted[int]{
	hardwareTierLow:        {{20 << 20, 0.35}, {30 << 20, 0.40}, {50 << 20, 0.25}},
	hardwareTierMainstream: {{30 << 20, 0.25}, {50 << 20, 0.45}, {80 << 20, 0.30}},
	hardwareTierHigh:       {{50 << 20, 0.25}, {80 << 20, 0.40}, {100 << 20, 0.35}},
}

// windowsMediaDeviceCounts 是 BrowserForge 指纹数据（apify_fingerprint_datapoints 0.10.0）中 Windows 桌面 Chrome 与 Edge 未授权时列出的麦克风、摄像头、扬声器个数组合的占比，每类至多一个
var windowsMediaDeviceCounts = []weighted[[3]int]{
	{[3]int{1, 1, 1}, 0.7974},
	{[3]int{1, 0, 1}, 0.0947},
	{[3]int{0, 0, 1}, 0.0710},
	{[3]int{0, 1, 1}, 0.0213},
	{[3]int{0, 0, 0}, 0.0138},
	{[3]int{1, 0, 0}, 0.0007},
	{[3]int{0, 1, 0}, 0.0006},
	{[3]int{1, 1, 0}, 0.0006},
}

// maximizedShare 是新指纹中 Firefox 窗口为最大化状态的比例
const maximizedShare = 0.7

// drawWindowsHardwareProfile 先抽机器档位，再联动抽取核数、显卡、分辨率/缩放、任务栏高度、音频采样率与存储配额
func drawWindowsHardwareProfile() hardwareProfile {
	tier := draw(windowsHardwareTiers)
	return drawWindowsHardwareProfileForTier(tier)
}

// drawWindowsHardwareProfileForTier 按指定档位抽取一组内部自洽的 Windows 硬件画像
func drawWindowsHardwareProfileForTier(tier hardwareTier) hardwareProfile {
	display := draw(tierDisplays[tier])
	return hardwareProfile{
		tier:         tier,
		concurrency:  draw(tierThreads[tier]),
		display:      display,
		scale:        draw(display.scales),
		taskbar:      draw(tierTaskbars[tier]),
		webgl:        windowsWebGLForTier(tier),
		audioRate:    draw(windowsAudioSampleRates),
		audioChans:   windowsAudioChannels,
		storageLimit: draw(tierStorageLimits[tier]),
	}
}

// applyWindowsDisplay 按抽到的主屏、缩放与任务栏高度写入 Windows 屏幕、可用区域、devicePixelRatio、外窗、内窗与窗口位置：可用区域为屏幕减 taskbar，最大化窗口位于 -b、外窗比可用区域大 2b，b 为 9 个物理像素的 CSS 像素数；普通窗口在可用区域内随机放置
func applyWindowsDisplay(config map[string]any, display windowsDisplay, scale float64, taskbar int) {
	width := int(math.Round(float64(display.width) / scale))
	height := int(math.Round(float64(display.height) / scale))
	availHeight := height - taskbar
	border := int(math.Round(9 / scale))
	config["screen.width"], config["screen.height"] = width, height
	config["screen.availWidth"], config["screen.availHeight"] = width, availHeight
	config["window.devicePixelRatio"] = scale
	if rand.Float64() < maximizedShare {
		config["window.screenX"], config["window.screenY"] = -border, -border
		config["window.outerWidth"], config["window.outerHeight"] = width+2*border, availHeight+2*border
		config["window.innerWidth"] = width
		config["window.innerHeight"] = availHeight + 2*border - int(math.Round(85+18/scale))
		return
	}
	outerWidth := int(math.Round(float64(width) * (0.55 + 0.4*rand.Float64())))
	outerHeight := int(math.Round(float64(availHeight) * (0.7 + 0.3*rand.Float64())))
	config["window.screenX"], config["window.screenY"] = rand.IntN(width-outerWidth+1), rand.IntN(availHeight-outerHeight+1)
	config["window.outerWidth"], config["window.outerHeight"] = outerWidth, outerHeight
	config["window.innerWidth"] = outerWidth - 2*border
	config["window.innerHeight"] = outerHeight - int(math.Round(83+11/scale))
}

// isLowEndWebGLRenderer 判断 WebGL 渲染器是否属于软件渲染或老旧/极低端显卡档位
func isLowEndWebGLRenderer(vendor, renderer string) bool {
	if vendor == "Google Inc. (Microsoft)" || strings.Contains(renderer, "Basic Render Driver") {
		return true
	}
	return strings.Contains(renderer, "vs_4_1") ||
		strings.Contains(renderer, "HD Graphics 400") ||
		strings.Contains(renderer, "Radeon HD 3200") ||
		strings.Contains(renderer, "GTX 480")
}

// windowsWebGLForTier 按硬件档位抽取匹配的 Firefox 158 模糊化 Windows WebGL 配置，中高配排除软件渲染与极低端集显
func windowsWebGLForTier(tier hardwareTier) map[string]any {
	vendor := draw(tierGPUVendors[tier])
	vendorName := "Google Inc. (" + vendor + ")"
	var profiles []weighted[map[string]any]
	for _, profile := range windowsWebGLProfiles {
		renderer, _ := profile.Config["webGl:renderer"].(string)
		if profile.Config["webGl:vendor"] != vendorName || !strings.HasSuffix(renderer, ", or similar") {
			continue
		}
		lowEnd := isLowEndWebGLRenderer(vendorName, renderer)
		if tier == hardwareTierLow {
			if vendor != "Intel" && !lowEnd {
				continue
			}
		} else if lowEnd {
			continue
		}
		profiles = append(profiles, weighted[map[string]any]{profile.Config, profile.Weight})
	}
	if len(profiles) == 0 {
		return windowsWebGLByVendor()
	}
	return maps.Clone(draw(profiles))
}

// windowsWebGLByVendor 按 Firefox 硬件报告的显卡厂商占比抽取一组 Firefox 158 模糊化后的 Windows WebGL 配置，同一厂商内按官方启动器的权重
func windowsWebGLByVendor() map[string]any {
	vendor := draw([]weighted[string]{{"Intel", firefoxGPUVendors["Intel"]}, {"AMD", firefoxGPUVendors["AMD"]}, {"NVIDIA", firefoxGPUVendors["NVIDIA"]}, {"Microsoft", firefoxGPUVendors["Microsoft"]}})
	var profiles []weighted[map[string]any]
	for _, profile := range windowsWebGLProfiles {
		renderer, _ := profile.Config["webGl:renderer"].(string)
		if profile.Config["webGl:vendor"] == "Google Inc. ("+vendor+")" && strings.HasSuffix(renderer, ", or similar") {
			profiles = append(profiles, weighted[map[string]any]{profile.Config, profile.Weight})
		}
	}
	return maps.Clone(draw(profiles))
}

// windowsMediaDevices 按 windowsMediaDeviceCounts 抽取 Camoufox 媒体设备个数，Firefox 158 未授权时每类输入设备至多列出一个、不列出扬声器
func windowsMediaDevices() map[string]any {
	counts := draw(windowsMediaDeviceCounts)
	return map[string]any{"mediaDevices:enabled": true, "mediaDevices:micros": counts[0], "mediaDevices:webcams": counts[1], "mediaDevices:speakers": counts[2]}
}

// windowsLocaleFonts 是 Windows 以该语言为显示语言时自动安装的补充字体，英文名与本地化名都列出
var windowsLocaleFonts = map[string][]string{
	"zh-TW": {"DFKai-SB", "標楷體", "MingLiU", "細明體", "PMingLiU", "新細明體", "MingLiU_HKSCS", "細明體_HKSCS"},
	"zh-CN": {"DengXian", "等线", "DengXian Light", "等线 Light", "FangSong", "仿宋", "KaiTi", "楷体", "SimHei", "黑体"},
	"ja":    {"Meiryo", "メイリオ", "Meiryo UI", "MS Mincho", "ＭＳ 明朝", "MS PMincho", "ＭＳ Ｐ明朝", "Yu Mincho", "游明朝"},
	"ko":    {"Batang", "바탕", "BatangChe", "바탕체", "Dotum", "돋움", "DotumChe", "돋움체", "Gulim", "굴림", "GulimChe", "굴림체", "Gungsuh", "궁서", "GungsuhChe", "궁서체"},
}

// localeFonts 返回语言对应的 Windows 补充字体，繁体中文地区共用 zh-TW
func localeFonts(locale string) []string {
	language, region, _ := strings.Cut(locale, "-")
	switch strings.ToLower(language) {
	case "zh":
		if region == "TW" || region == "HK" || region == "MO" {
			return windowsLocaleFonts["zh-TW"]
		}
		return windowsLocaleFonts["zh-CN"]
	case "ja", "ko":
		return windowsLocaleFonts[strings.ToLower(language)]
	}
	return nil
}
