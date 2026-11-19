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
export function tierAllows(model: Model, accounts: readonly Account[]): boolean {
  const modes = model.access_modes ?? []
  if (modes.length === 0) return true
  const tiers = new Set(accounts.map((account) => account.benefit_tier))
  return modes.some(
    (mode) =>
      (mode === 3 && (tiers.has('Pro') || tiers.has('Ultra'))) ||
      (mode === 4 && tiers.has('Ultra')),
  )
}

export type AccountTestTier = 'Pro' | 'Ultra'

export interface AccountTestOption {
  id: string
  name: string
  tier: AccountTestTier | null
  pinned: boolean
  aliases: string[]
}

export type AccountTestSupport =
  | { supported: true }
  | { supported: false; reason: 'tier'; requiredTier: AccountTestTier }
  | { supported: false; reason: 'missing' }

// modelTierRequirement 返回模型对订阅档位的最低要求：仅 Ultra 为 'Ultra'，支持 Pro/Ultra 为 'Pro'，其余为 null
export function modelTierRequirement(model: Model | undefined): AccountTestTier | null {
  const modes = model?.access_modes ?? []
  if (modes.length === 0) return null
  if (modes.includes(3)) return 'Pro'
  if (modes.includes(4)) return 'Ultra'
  return null
}

// buildAccountTestOptions 生成账户页测试模型选项：置顶配置项排在前面，其余按目录顺序列出所有当前账户可用对话模型
export function buildAccountTestOptions(
  models: readonly Model[],
  accounts: readonly Account[],
  pinnedValue: string | undefined,
): AccountTestOption[] {
  const testable = new Map<string, Model>()
  for (const model of models) {
    if (accountTestable(model) && tierAllows(model, accounts)) testable.set(model.id, model)
  }
  const pinnedIDs = parseTestModels(pinnedValue)
  const pinnedSet = new Set(pinnedIDs)
  const toOption = (id: string, model: Model | undefined, pinned: boolean): AccountTestOption => ({
    id,
    name: model?.name || id,
    tier: modelTierRequirement(model),
    pinned,
    aliases: model?.capability_options?.aliases ?? [],
  })
  const result: AccountTestOption[] = []
  const seen = new Set<string>()
  for (const id of pinnedIDs) {
    seen.add(id)
    result.push(toOption(id, testable.get(id), true))
  }
  for (const model of testable.values()) {
    if (seen.has(model.id)) continue
    result.push(toOption(model.id, model, pinnedSet.has(model.id)))
  }
  return result
}

// accountTestSupport 判断指定账户是否支持用 modelID 做对话测试，用于在界面上给出防呆提示
export function accountTestSupport(
  account: Account,
  modelID: string,
  model: Model | undefined,
): AccountTestSupport {
  if (modelID === '') return { supported: true }
  const requiredTier = modelTierRequirement(model)
  if (requiredTier !== null && account.benefit_tier !== 'Unknown') {
    if (requiredTier === 'Ultra' && account.benefit_tier !== 'Ultra') {
      return { supported: false, reason: 'tier', requiredTier: 'Ultra' }
    }
    if (
      requiredTier === 'Pro' &&
      account.benefit_tier !== 'Pro' &&
      account.benefit_tier !== 'Ultra'
    ) {
      return { supported: false, reason: 'tier', requiredTier: 'Pro' }
    }
  }
  if (account.models.length > 0) {
    const candidates = [modelID, ...(model?.capability_options?.aliases ?? [])]
    if (!candidates.some((id) => account.models.includes(id))) {
      return { supported: false, reason: 'missing' }
    }
  }
  return { supported: true }
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
