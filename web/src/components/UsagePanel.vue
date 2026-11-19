<script setup lang="ts">
import { computed, onActivated, onDeactivated, provide, ref, shallowRef, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type {
  Account,
  Cooldown,
  Model,
  RequestSummary,
  UsageDimension,
  UsageReport,
  UsageStats,
} from '@/types'
import UiIcon from './UiIcon.vue'
import UiSelect, { keepSelected } from './UiSelect.vue'
import FilterMenu from './FilterMenu.vue'
import RequestsPanel from './RequestsPanel.vue'
import KpiCard from './usage/KpiCard.vue'
import RangePicker from './usage/RangePicker.vue'
import RankingTable from './usage/RankingTable.vue'
import RecordsTable from './usage/RecordsTable.vue'
import RefreshButton from './usage/RefreshButton.vue'
import UsageCharts from './usage/UsageCharts.vue'
import { usageClock, useUsageFormat } from './usage/format'
import { readPreference, writePreference } from '@/preference'
import {
  dimensions,
  liveRange,
  rateTone,
  resolveRange,
  successRate,
  useUsageState,
  type RangePreset,
} from './usage/model'

type SubTab = 'realtime' | 'accounts' | 'cooldowns' | 'charts'

const props = withDefaults(
  defineProps<{
    models: Model[]
    accounts?: Account[]
    cooldowns?: Cooldown[]
    requests?: RequestSummary[]
    requestsLoading?: boolean
    cooldownError?: string
    requestError?: string
    initialSubTab?: SubTab
  }>(),
  {
    accounts: () => [],
    cooldowns: () => [],
    requests: () => [],
    requestsLoading: false,
    cooldownError: '',
    requestError: '',
    initialSubTab: 'realtime',
  },
)

const emit = defineEmits<{
  refreshRequests: []
  notice: [message: string, tone: 'success' | 'error']
}>()

const { t, errorText } = useI18n()
const { compact, integer, usd, percent, duration, dateTime, dimensionLabel } = useUsageFormat()
const state = useUsageState()

const subTab = ref<SubTab>(
  readPreference<SubTab>('usage.subTab', props.initialSubTab, (value) =>
    ['realtime', 'accounts', 'cooldowns', 'charts'].includes(value as string),
  ),
)
watch(subTab, (value) => writePreference('usage.subTab', value))
watch(
  () => props.initialSubTab,
  (value) => {
    if (value === 'cooldowns') subTab.value = 'cooldowns'
  },
)

const onlyFailed = ref(false)
const quickSearch = ref(state.search)
watch(
  () => state.search,
  (value) => {
    quickSearch.value = value
  },
)
let quickSearchTimer: number | undefined
watch(quickSearch, (value) => {
  window.clearTimeout(quickSearchTimer)
  quickSearchTimer = window.setTimeout(() => {
    state.search = value.trim()
  }, 260)
})

const quickRanges: RangePreset[] = ['today', 'quotaCycle', '7d', '30d', '90d']

function filterOptions(dimension: UsageDimension): string[] {
  const options = report.value?.options[dimension] ?? []
  if (dimension !== 'model') return options
  const order = new Map(props.models.map((model, index) => [model.id, index]))
  const rank = (value: string) => order.get(value) ?? order.size
  return [...options].sort((left, right) => rank(left) - rank(right))
}

const report = shallowRef<UsageReport | null>(null)
const status = ref<'loading' | 'ready' | 'error'>('loading')
const loading = ref(false)
const error = ref('')
const updatedAt = ref(0)
const now = ref(Date.now())
provide(usageClock, now)
const recordsRefresh = ref(0)
const refreshChoices = [0, 5, 15, 30, 60] as const
const autoRefresh = ref<number>(
  readPreference('usage.autoRefresh', 15, (value) =>
    refreshChoices.includes(value as (typeof refreshChoices)[number]),
  ),
)
watch(autoRefresh, (value) => writePreference('usage.autoRefresh', value))
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
let controller: AbortController | undefined
let clockTimer: number | undefined

const minute = computed(() => Math.floor(now.value / 60_000) * 60_000)
const resolved = computed(() => resolveRange(state, new Date(minute.value)))
const live = computed(() => liveRange(state.range))
const hasFilters = computed(
  () =>
    dimensions.some((item) => (state.filters[item] ?? []).length > 0) ||
    state.search !== '' ||
    state.status !== '' ||
    onlyFailed.value,
)
const scope = computed(() => JSON.stringify([state.range, state.from, state.to, state.filters]))

function baseQuery(): URLSearchParams | null {
  const range = resolveRange(state, new Date())
  if (range === null) return null
  const query = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() })
  for (const dimension of dimensions) {
    const values = state.filters[dimension] ?? []
    if (values.length > 0) query.set(dimension, values.join(','))
  }
  return query
}

async function load(): Promise<void> {
  const query = baseQuery()
  if (query === null) {
    status.value = report.value === null ? 'error' : status.value
    error.value = t('usage.rangeInvalid')
    return
  }
  query.set('tz', timezone)
  query.set('stack', state.stack)
  query.set('pairs', '0')
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const value = await api.usage(query, current.signal)
    report.value = value
    status.value = 'ready'
    error.value = ''
    updatedAt.value = Date.now()
  } catch (reason) {
    if (current.signal.aborted) return
    error.value = errorText(reason)
    if (report.value === null) status.value = 'error'
  } finally {
    if (controller === current) loading.value = false
  }
}

async function loadPairs(
  dimension: 'account' | 'model',
  key: string,
): Promise<UsageReport['pairs']> {
  const query = baseQuery()
  if (query === null) throw new Error(t('usage.rangeInvalid'))
  query.set(dimension, key)
  query.set('tz', timezone)
  query.set('stack', state.stack)
  return (await api.usage(query, new AbortController().signal)).pairs
}

function refresh(): void {
  void load()
  recordsRefresh.value++
  emit('refreshRequests')
}

watch(
  () => JSON.stringify([state.range, state.from, state.to, state.stack, state.filters]),
  () => void load(),
)

function tick(): void {
  now.value = Date.now()
  if (
    autoRefresh.value > 0 &&
    live.value &&
    document.visibilityState === 'visible' &&
    !loading.value &&
    updatedAt.value > 0 &&
    now.value - updatedAt.value >= autoRefresh.value * 1000
  )
    refresh()
}

function resume(): void {
  if (document.visibilityState === 'visible') tick()
}

onActivated(() => {
  if (report.value === null) void load()
  else tick()
  clockTimer = window.setInterval(tick, 1000)
  document.addEventListener('visibilitychange', resume)
})
onDeactivated(() => {
  controller?.abort()
  window.clearInterval(clockTimer)
  window.clearTimeout(quickSearchTimer)
  document.removeEventListener('visibilitychange', resume)
})

function applyRange(range: RangePreset, from: string, to: string): void {
  Object.assign(state, { range, from, to })
}

function setFilter(dimension: UsageDimension, values: string[]): void {
  const filters = { ...state.filters }
  if (values.length === 0) delete filters[dimension]
  else filters[dimension] = values
  state.filters = filters
}

function addFilter(dimension: UsageDimension, value: string): void {
  const values = state.filters[dimension] ?? []
  if (!values.includes(value)) setFilter(dimension, [...values, value])
}

function addFilterPair(account: string, model: string): void {
  const filters = { ...state.filters }
  const accounts = filters.account ?? []
  const models = filters.model ?? []
  if (!accounts.includes(account)) filters.account = [...accounts, account]
  if (!models.includes(model)) filters.model = [...models, model]
  state.filters = filters
}

function clearFilters(): void {
  state.filters = {}
  state.status = ''
  state.search = ''
  quickSearch.value = ''
  onlyFailed.value = false
}

const activeChips = computed(() =>
  dimensions.flatMap((dimension) =>
    (state.filters[dimension] ?? []).map((value) => ({ dimension, value })),
  ),
)

function relative(current: number, previous: number): number | null {
  return previous > 0 ? (current - previous) / previous : null
}

function delta(
  change: number | null,
  text: (value: number) => string,
  better: 'up' | 'down' | 'none',
):
  | {
      text: string
      tone: 'good' | 'bad' | 'neutral'
      direction: 'up' | 'down'
      help: string
    }
  | undefined {
  const value = report.value
  if (change === null || value === null || value.previous.requests === 0) return undefined
  const span = new Date(value.to).getTime() - new Date(value.from).getTime()
  const range = `${dateTime(new Date(new Date(value.from).getTime() - span))} – ${dateTime(value.from)}`
  const flat = Math.abs(change) < 0.0005
  return {
    text: `${change >= 0 ? '+' : '−'}${text(Math.abs(change))}`,
    tone: flat || better === 'none' ? 'neutral' : change > 0 === (better === 'up') ? 'good' : 'bad',
    direction: change >= 0 ? 'up' : 'down',
    help: t('usage.vsPrevious').replace('{range}', range),
  }
}

const kpis = computed(() => {
  const value = report.value
  if (value === null) return []
  const { totals, previous, buckets, recent } = value
  const rate = successRate(totals)
  const previousRate = successRate(previous)
  const series = (pick: (bucket: UsageStats) => number | null) =>
    buckets.map(pick).filter((item): item is number => item !== null)
  const otherErrors = totals.failed - totals.rate_limited
  const first = totals.first_event
  const outTokens = totals.reasoning_tokens + totals.reply_tokens
  const cacheRatio = totals.input_tokens > 0 ? totals.cached_tokens / totals.input_tokens : null

  return [
    {
      label: t('usage.requests'),
      icon: 'requests' as const,
      iconBg: 'bg-blue-500/12 text-blue-400',
      value: integer(totals.requests),
      raw: totals.requests,
      help: t('usage.requestsHelp'),
      details: [
        { label: t('usage.succeeded'), value: integer(totals.succeeded) },
        { label: t('usage.canceled'), value: integer(totals.canceled) },
      ],
      delta: delta(relative(totals.requests, previous.requests), (item) => percent(item), 'none'),
      spark: buckets.map((bucket) => bucket.requests),
      color: '#3b82f6',
    },
    {
      label: t('usage.successRate'),
      icon: 'success' as const,
      iconBg: 'bg-green-500/12 text-green-400',
      value: percent(rate),
      raw: rate,
      help: t('usage.successRateHelp'),
      details: [
        { label: `${t('usage.firstEvent')} P50`, value: duration(first.p50_ms) },
        { label: 'P95', value: duration(first.p95_ms) },
      ],
      delta: delta(
        rate === null || previousRate === null ? null : rate - previousRate,
        (item) => t('usage.points').replace('{value}', (item * 100).toFixed(1)),
        'up',
      ),
      spark: series((bucket) => {
        const item = successRate(bucket)
        return item === null ? null : item * 100
      }),
      color: '#22c55e',
      tone: rateTone(rate),
    },
    {
      label: t('usage.failedCard'),
      icon: 'warning' as const,
      iconBg: 'bg-red-500/12 text-red-400',
      value: integer(totals.failed),
      raw: totals.failed,
      help: `${t('usage.rateLimitedShort')} + ${t('usage.otherErrors')}`,
      details: [
        {
          label: t('usage.rateLimitedShort'),
          value: integer(totals.rate_limited),
          tone: totals.rate_limited > 0 ? 'text-yellow-400' : undefined,
        },
        {
          label: t('cooldowns.title'),
          value: integer(props.cooldowns.length),
          tone: props.cooldowns.length > 0 ? 'text-red-400' : undefined,
        },
      ],
      delta: delta(relative(totals.failed, previous.failed), (item) => percent(item), 'down'),
      spark: buckets.map((bucket) => bucket.failed),
      color: '#ef4444',
      tone: totals.failed > 0 ? 'text-red-400' : 'text-white',
    },
    {
      label: t('usage.cost'),
      icon: 'cost' as const,
      iconBg: 'bg-amber-500/12 text-amber-400',
      value: value.cost === 0 && value.unpriced_models.length > 0 ? '—' : usd(value.cost),
      raw: value.cost === 0 && value.unpriced_models.length > 0 ? null : value.cost,
      help:
        value.unpriced_models.length > 0
          ? `${t('usage.costHelp')} · ${t('usage.unpriced')} ${value.unpriced_models.join(', ')}`
          : t('usage.costHelp'),
      details:
        value.cost === 0 && value.unpriced_models.length > 0
          ? [
              {
                label: t('usage.unpriced'),
                value: value.unpriced_models.join(', '),
                tone: 'text-amber-400',
              },
            ]
          : [
              {
                label: t('usage.otherErrors'),
                value: integer(otherErrors),
                tone: otherErrors > 0 ? 'text-red-400' : undefined,
              },
            ],
      delta: delta(relative(value.cost, value.previous_cost), (item) => percent(item), 'none'),
      spark: buckets.map((bucket) => bucket.cost),
      color: '#f59e0b',
    },
    {
      label: t('usage.tokens'),
      icon: 'tokens' as const,
      iconBg: 'bg-purple-500/12 text-purple-400',
      value: compact(totals.total_tokens),
      raw: totals.total_tokens,
      help: `${t('usage.input')} + ${t('usage.output')}`,
      details: [
        { label: `${recent.minutes}m`, value: `${compact(recent.requests / recent.minutes)} RPM` },
        { label: 'TPM', value: compact(recent.tokens / recent.minutes) },
      ],
      delta: delta(
        relative(totals.total_tokens, previous.total_tokens),
        (item) => percent(item),
        'none',
      ),
      spark: buckets.map((bucket) => bucket.total_tokens),
      color: '#8b5cf6',
    },
    {
      label: t('usage.input'),
      icon: 'throughput' as const,
      iconBg: 'bg-sky-500/12 text-sky-400',
      value: compact(totals.input_tokens),
      raw: totals.input_tokens,
      help: t('usage.input'),
      details: [
        {
          label: t('usage.tokens'),
          value:
            totals.total_tokens > 0
              ? percent(totals.input_tokens / totals.total_tokens)
              : '—',
        },
      ],
      delta: delta(
        relative(totals.input_tokens, previous.input_tokens),
        (item) => percent(item),
        'none',
      ),
      spark: buckets.map((bucket) => bucket.input_tokens),
      color: '#0ea5e9',
    },
    {
      label: t('usage.output'),
      icon: 'firstEvent' as const,
      iconBg: 'bg-indigo-500/12 text-indigo-400',
      value: compact(outTokens),
      raw: outTokens,
      help: `${t('usage.reasoning')} + ${t('usage.reply')}`,
      details: [
        { label: t('usage.reasoning'), value: compact(totals.reasoning_tokens) },
        { label: t('usage.reply'), value: compact(totals.reply_tokens) },
      ],
      delta: delta(
        relative(outTokens, previous.reasoning_tokens + previous.reply_tokens),
        (item) => percent(item),
        'none',
      ),
      spark: buckets.map((bucket) => bucket.reasoning_tokens + bucket.reply_tokens),
      color: '#6366f1',
    },
    {
      label: t('usage.cachedCard'),
      icon: 'cached' as const,
      iconBg: 'bg-teal-500/12 text-cyan-400',
      highlight: true,
      value: compact(totals.cached_tokens),
      raw: totals.cached_tokens,
      help: t('usage.cacheHitRate'),
      details: [
        {
          label: t('usage.cacheHitRateShort'),
          value: cacheRatio !== null ? percent(cacheRatio) : '—',
          tone: 'text-cyan-400 font-semibold',
        },
      ],
      delta: delta(
        relative(totals.cached_tokens, previous.cached_tokens),
        (item) => percent(item),
        'up',
      ),
      spark: buckets.map((bucket) => bucket.cached_tokens),
      color: '#14b8a6',
      tone: 'text-cyan-400',
    },
  ]
})

const statusOptions = computed(() =>
  keepSelected(
    (report.value?.groups.status ?? []).map((group) => group.key).sort(),
    state.status,
    '',
  ),
)

const liveAndCooldownCount = computed(() => props.requests.length + props.cooldowns.length)
</script>

<template>
  <section class="relative flex min-h-0 flex-1 flex-col overflow-auto [scrollbar-gutter:stable]">
    <div class="@container mx-auto w-full max-w-[90rem] space-y-4 px-4 py-4 md:px-6 md:py-5">
      <!-- CPA Manager Style Two-Row Filter Toolbar Card -->
      <div
        class="space-y-3 rounded-2xl border border-line bg-panel p-3.5 shadow-sm"
        role="toolbar"
        :aria-label="t('usage.filters')"
      >
        <div class="flex flex-wrap items-center gap-2">
          <div class="inline-flex flex-wrap items-center gap-0.5 rounded-xl border border-line bg-canvas p-1">
            <button
              v-for="preset in quickRanges"
              :key="preset"
              type="button"
              class="rounded-lg px-2.5 py-1 text-xs font-medium transition"
              :class="
                state.range === preset
                  ? 'bg-panel text-blue-400 shadow-sm font-semibold'
                  : 'text-gray-400 hover:text-white'
              "
              @click="applyRange(preset, '', '')"
            >
              {{ t(`usage.range.${preset}`) }}
            </button>
          </div>

          <RangePicker :range="state.range" :resolved="resolved" @apply="applyRange" />

          <label for="monitor-quick-search" class="relative min-w-[14rem] flex-1">
            <span class="sr-only">{{ t('usage.recordsSearch') }}</span>
            <UiIcon
              name="search"
              :size="14"
              class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-gray-500"
            />
            <input
              id="monitor-quick-search"
              v-model="quickSearch"
              type="search"
              :placeholder="t('usage.searchQuickPlaceholder')"
              class="h-8.5 w-full rounded-xl border border-line bg-canvas pr-3 pl-8 text-xs text-white focus:border-blue-500 focus:outline-none"
            />
          </label>

          <div class="flex items-center gap-1.5">
            <UiSelect
              v-if="live"
              :model-value="autoRefresh"
              :aria-label="t('usage.autoRefresh')"
              class="h-8.5 w-max rounded-xl border border-line bg-canvas px-2.5 text-xs text-gray-200 hover:border-line-strong"
              @update:model-value="autoRefresh = Number($event)"
            >
              <option v-for="choice in refreshChoices" :key="choice" :value="choice">
                {{ choice === 0 ? t('usage.autoRefresh.0') : `Auto ${choice}s` }}
              </option>
            </UiSelect>

            <div class="flex h-8.5 items-stretch rounded-xl border border-line bg-canvas text-xs">
              <RefreshButton
                :loading="loading"
                :live="live"
                :updated-at="updatedAt"
                :latest-at="report?.latest_at"
                @refresh="refresh"
              />
            </div>

            <button
              v-if="hasFilters"
              type="button"
              class="h-8.5 rounded-xl border border-line bg-canvas px-3 text-xs font-medium text-gray-300 hover:border-line-strong hover:text-white"
              @click="clearFilters"
            >
              {{ t('usage.resetFilters') }}
            </button>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <FilterMenu
            v-for="dimension in dimensions"
            :key="dimension"
            :label="t(`usage.dimension.${dimension}`)"
            :options="filterOptions(dimension)"
            :selected="state.filters[dimension] ?? []"
            :format="(value: string) => dimensionLabel(dimension, value)"
            :search-label="t('usage.filterSearch')"
            :empty-label="t('usage.filterNone')"
            :clear-label="t('usage.filtersClear')"
            @change="setFilter(dimension, $event)"
          />
        </div>

        <div v-if="activeChips.length > 0" class="flex flex-wrap gap-1.5 pt-1">
          <span
            v-for="chip in activeChips"
            :key="`${chip.dimension}:${chip.value}`"
            class="flex max-w-full items-center gap-1 rounded-full border border-blue-500/40 bg-blue-600/10 py-0.5 pr-1.5 pl-2.5 text-xs text-blue-300"
          >
            <span class="shrink-0 opacity-75">{{ t(`usage.dimension.${chip.dimension}`) }}:</span>
            <span
              v-tooltip.overflow="dimensionLabel(chip.dimension, chip.value)"
              class="min-w-0 truncate"
              >{{ dimensionLabel(chip.dimension, chip.value) }}</span
            >
            <button
              type="button"
              class="shrink-0 rounded-full p-0.5 hover:bg-blue-500/20"
              :aria-label="`${t('common.close')} ${dimensionLabel(chip.dimension, chip.value)}`"
              @click="
                setFilter(
                  chip.dimension,
                  (state.filters[chip.dimension] ?? []).filter((item) => item !== chip.value),
                )
              "
            >
              <UiIcon name="close" :size="11" />
            </button>
          </span>
        </div>
      </div>

      <div
        v-if="error && status !== 'error'"
        class="flex items-center gap-3 rounded-2xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-400"
        role="alert"
      >
        <UiIcon name="warning" :size="16" />
        <span class="flex-1">{{ error }}</span>
        <button
          type="button"
          class="rounded-lg border border-red-500/40 px-3 py-1 text-xs hover:bg-red-500/10"
          @click="load()"
        >
          {{ t('usage.retry') }}
        </button>
      </div>

      <template v-if="status === 'loading'">
        <div
          class="grid grid-cols-1 gap-3.5 @min-[37rem]:grid-cols-2 @min-[58rem]:grid-cols-4"
          aria-hidden="true"
        >
          <div v-for="index in 8" :key="index" class="h-32 animate-pulse rounded-2xl bg-panel"></div>
        </div>
        <div class="h-80 animate-pulse rounded-2xl bg-panel" aria-hidden="true"></div>
        <p class="sr-only" role="status">{{ t('common.loading') }}</p>
      </template>

      <div
        v-else-if="status === 'error'"
        class="flex flex-col items-center gap-3 rounded-2xl border border-red-500/40 bg-red-500/5 px-6 py-16 text-center"
        role="alert"
      >
        <UiIcon name="warning" :size="28" class="text-red-400" />
        <p class="text-sm text-red-400">{{ error }}</p>
        <button
          type="button"
          class="rounded-lg border border-red-500/40 px-3 py-1 text-xs text-red-300 hover:bg-red-500/10"
          @click="load()"
        >
          {{ t('usage.retry') }}
        </button>
      </div>

      <template v-else-if="report !== null">
        <div class="space-y-4" :aria-busy="loading">
          <!-- 8 KPI Cards (2 x 4) -->
          <div class="grid grid-cols-1 gap-3.5 @min-[37rem]:grid-cols-2 @min-[58rem]:grid-cols-4">
            <KpiCard
              v-for="kpi in kpis"
              :key="kpi.label"
              :label="kpi.label"
              :icon="kpi.icon"
              :icon-bg="kpi.iconBg"
              :highlight="kpi.highlight"
              :value="kpi.value"
              :raw="kpi.raw"
              :help="kpi.help"
              :details="kpi.details"
              :delta="kpi.delta"
              :spark="kpi.spark"
              :color="kpi.color"
              :tone="kpi.tone"
            />
          </div>

          <!-- Tabbed Monitor Workspace -->
          <section class="overflow-hidden rounded-2xl border border-line bg-panel shadow-sm">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-canvas/40 px-4 py-2.5">
              <div class="flex flex-wrap items-center gap-1.5">
                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-xl border px-3 py-1.5 text-xs font-medium transition"
                  :class="
                    subTab === 'realtime'
                      ? 'border-blue-500/30 bg-blue-500/10 text-blue-400 font-semibold'
                      : 'border-transparent text-gray-400 hover:bg-raised hover:text-white'
                  "
                  @click="subTab = 'realtime'"
                >
                  {{ t('usage.subRealtime') }}
                  <span class="rounded-full border border-line bg-panel px-1.5 py-0.2 text-[11px] tabular-nums">
                    {{ integer(report.totals.requests) }}
                  </span>
                </button>

                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-xl border px-3 py-1.5 text-xs font-medium transition"
                  :class="
                    subTab === 'accounts'
                      ? 'border-blue-500/30 bg-blue-500/10 text-blue-400 font-semibold'
                      : 'border-transparent text-gray-400 hover:bg-raised hover:text-white'
                  "
                  @click="subTab = 'accounts'"
                >
                  {{ t('usage.subAccounts') }}
                  <span class="rounded-full border border-line bg-panel px-1.5 py-0.2 text-[11px] tabular-nums">
                    {{ report.groups.account.length }}
                  </span>
                </button>

                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-xl border px-3 py-1.5 text-xs font-medium transition"
                  :class="
                    subTab === 'cooldowns'
                      ? 'border-blue-500/30 bg-blue-500/10 text-blue-400 font-semibold'
                      : 'border-transparent text-gray-400 hover:bg-raised hover:text-white'
                  "
                  @click="subTab = 'cooldowns'"
                >
                  {{ t('usage.subCooldowns') }}
                  <span
                    class="rounded-full border px-1.5 py-0.2 text-[11px] tabular-nums"
                    :class="
                      liveAndCooldownCount > 0
                        ? 'border-amber-500/40 bg-amber-500/10 text-amber-400'
                        : 'border-line bg-panel text-gray-400'
                    "
                  >
                    {{ liveAndCooldownCount }}
                  </span>
                </button>

                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-xl border px-3 py-1.5 text-xs font-medium transition"
                  :class="
                    subTab === 'charts'
                      ? 'border-blue-500/30 bg-blue-500/10 text-blue-400 font-semibold'
                      : 'border-transparent text-gray-400 hover:bg-raised hover:text-white'
                  "
                  @click="subTab = 'charts'"
                >
                  {{ t('usage.subCharts') }}
                </button>
              </div>
            </div>

            <div v-if="subTab === 'realtime'">
              <RecordsTable
                embedded
                :scope="scope"
                :base-query="baseQuery"
                :status="state.status"
                :search="state.search"
                :total="report.totals.requests"
                :status-options="statusOptions"
                :refresh-key="recordsRefresh"
                :only-failed="onlyFailed"
                @update:status="state.status = $event"
                @update:search="state.search = $event"
                @update:only-failed="onlyFailed = $event"
              />
            </div>

            <div v-else-if="subTab === 'accounts'" class="p-4">
              <div
                v-if="report.totals.requests === 0"
                class="flex flex-col items-center justify-center py-14 text-center"
              >
                <UiIcon name="empty" :size="32" class="mb-2 text-gray-600" />
                <p class="text-sm text-gray-400">{{ hasFilters ? t('usage.emptyFiltered') : t('usage.empty') }}</p>
              </div>
              <RankingTable
                v-else
                :report="report"
                :load-pairs="loadPairs"
                :accounts="accounts"
                :cooldowns="cooldowns"
                @filter="addFilter"
                @filter-pair="addFilterPair"
                @status="state.status = $event"
              />
            </div>

            <div v-else-if="subTab === 'cooldowns'">
              <RequestsPanel
                :accounts="accounts"
                :cooldowns="cooldowns"
                :requests="requests"
                :loading="requestsLoading"
                :cooldown-error="cooldownError"
                :request-error="requestError"
                @refresh="emit('refreshRequests')"
                @notice="(msg, tone) => emit('notice', msg, tone)"
              />
            </div>

            <div v-else-if="subTab === 'charts'" class="p-4">
              <div
                v-if="report.totals.requests === 0"
                class="flex flex-col items-center justify-center py-14 text-center"
              >
                <UiIcon name="empty" :size="32" class="mb-2 text-gray-600" />
                <p class="text-sm text-gray-400">{{ hasFilters ? t('usage.emptyFiltered') : t('usage.empty') }}</p>
              </div>
              <UsageCharts
                v-else
                :report="report"
                :stack="state.stack"
                @update:stack="state.stack = $event"
              />
            </div>
          </section>
        </div>
      </template>
    </div>
  </section>
</template>
