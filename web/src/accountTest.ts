import type { Account, Model } from '@/types'

// accountTestable 判断模型能否用一句文字请求做账户对话测试：支持 generateContent 的对话模型，排除图片、视频、音乐、语音、实时、转写、写代码助手、后台任务与搜索别名
export function accountTestable(model: Model): boolean {
  const caps = model.capabilities ?? {}
  return (
    model.methods.includes('generateContent') &&
    caps.chat_model === true &&
    !excludedCapabilities.some((name) => caps[name] === true) &&
    !model.id.endsWith('-search')
  )
}

const excludedCapabilities = [
  'image_route',
  'video_route',
  'music_route',
  'speech_route',
  'live_route',
  'assistant_route',
  'interactions_api',
  'transcription_output',
]

// tierAllows 判断账户中是否有权益等级能用该模型：access_modes 为空时所有等级可用，3 需要 Pro 或 Ultra，4 需要 Ultra
export function tierAllows(model: Model, accounts: Account[]): boolean {
  const modes = model.access_modes ?? []
  if (modes.length === 0) return true
  const tiers = new Set(accounts.map((account) => account.benefit_tier))
  return modes.some(
    (mode) =>
      (mode === 3 && (tiers.has('Pro') || tiers.has('Ultra'))) ||
      (mode === 4 && tiers.has('Ultra')),
  )
}

// parseTestModels 把 ACCOUNT_TEST_MODELS 拆成去重后按顺序排列的模型 ID
export function parseTestModels(value: string | undefined): string[] {
  return [
    ...new Set(
      (value ?? '')
        .split(',')
        .map((id) => id.trim())
        .filter(Boolean),
    ),
  ]
}
