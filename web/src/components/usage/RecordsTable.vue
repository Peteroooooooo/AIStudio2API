<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, useId, watch } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { UsageRecord } from '@/types'
import UiIcon from '../UiIcon.vue'
import UiSelect from '../UiSelect.vue'
import RecordDrawer from './RecordDrawer.vue'
import { useUsageFormat } from './format'
import { readPreference, writePreference } from '@/preference'
import { latencyTone } from './model'

type Column =
  | 'account'
  | 'model'
  | 'tokens'
  | 'output'
  | 'tps'
  | 'latency'
  | 'attempts'
  | 'queue'

const props = defineProps<{
  scope: string
  baseQuery: () => URLSearchParams | null
  status: string
  search: string
  total: number
  statusOptions: string[]
  refreshKey: number
  onlyFailed?: boolean
  embedded?: boolean
}>()
const emit = defineEmits<{
  'update:status': [value: string]
  'update:search': [value: string]
  'update:onlyFailed': [value: boolean]
}>()

const { t, errorText } = useI18n()
const { compact, integer, duration, dateTime, dimensionLabel } = useUsageFormat()
const id = useId()
const pageSizes = [20, 50, 100]
const allColumns: Column[] = [
  'account',
  'model',
  'tokens',
  'output',
  'tps',
  'latency',
  'attempts',
  'queue',
]
const columnLabels: Record<Column, () => string> = {
  account: () => t('usage.dimension.account'),
  model: () => t('usage.dimension.model'),
  tokens: () => `${t('usage.input')} / Cached`,
  output: () => t('usage.output'),
  tps: () => 'TPS',
  latency: () => t('usage.duration'),
  attempts: () => t('usage.attempts'),
  queue: () => t('usage.queue'),
}
const secondaryClass = 'hidden @min-[76rem]:table-cell'
const pageSize = ref(
  readPreference('usage.pageSize', 20, (value) => pageSizes.includes(value as number)),
)
const masked = ref(readPreference('usage.masked', false, (value) => typeof value === 'boolean'))
const visible = ref<Column[]>(
  readPreference<Column[]>(
    'usage.columns.v2',
    ['account', 'model', 'tokens', 'output', 'tps', 'latency'],
    (value) => Array.isArray(value) && value.every((item) => allColumns.includes(item as Column)),
  ),
)
watch(pageSize, (value) => writePreference('usage.pageSize', value))
watch(masked, (value) => writePreference('usage.masked', value))
watch(visible, (value) => writePreference('usage.columns.v2', value), { deep: true })

const items = shallowRef<UsageRecord[]>([])
const cursors = ref<(string | undefined)[]>([undefined])
const page = ref(0)
const nextCursor = ref<string | undefined>()
const state = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const selected = ref<UsageRecord | null>(null)
const searchText = ref(props.search)
let controller: AbortController | undefined
let searchTimer: number | undefined

const filtered = computed(() => props.status !== '' || props.search !== '' || props.onlyFailed)

const displayedItems = computed(() =>
  props.onlyFailed
    ? items.value.filter(
        (record) => record.state === 'failed' || record.status >= 400,
      )
    : items.value,
)

function maskAccount(value: string): string {
  if (!value) return '—'
  if (!masked.value) return value
  const at = value.indexOf('@')
  if (at > 0) return `${value.slice(0, Math.min(3, at))}***${value.slice(at)}`
  return value.length > 6 ? `${value.slice(0, 3)}***${value.slice(-2)}` : '***'
}

function cacheHitPct(record: UsageRecord): string {
  if (record.input_tokens <= 0) return '0.0%'
  return `${((record.cached_tokens / record.input_tokens) * 100).toFixed(1)}%`
}

function recordTps(record: UsageRecord): string {
  const out = record.reasoning_tokens + record.reply_tokens
  if (out <= 0 || record.duration_ms <= 0) return '—'
  if (record.first_output !== undefined && record.first_output >= 0) {
    const streamTokens = out - record.first_output
    const genMs = record.duration_ms - Math.max(0, record.first_event_ms)
    if (record.first_event_ms <= 0 || streamTokens <= 0 || genMs < 200) return '—'
    return String(Math.round(streamTokens / (genMs / 1000)))
  }
  const execMs = Math.max(200, record.duration_ms - Math.max(0, record.queue_ms))
  return String(Math.round(out / (execMs / 1000)))
}

function parameters(cursor?: string): URLSearchParams | null {
  const query = props.baseQuery()
  if (query === null) return null
  if (props.status !== '') query.set('status', props.status)
  if (props.search !== '') query.set('q', props.search)
  if (props.onlyFailed && !query.has('state')) query.set('state', 'failed')
  if (cursor !== undefined) query.set('cursor', cursor)
  return query
}

async function load(): Promise<void> {
  controller?.abort()
  const current = new AbortController()
  controller = current
  state.value = items.value.length === 0 ? 'loading' : state.value
  error.value = ''
  const query = parameters(cursors.value[page.value])
  if (query === null) return
  query.set('limit', String(pageSize.value))
  try {
    const result = await api.usageRecords(query, current.signal)
    items.value = result.items
    nextCursor.value = result.next_cursor
    state.value = 'ready'
  } catch (reason) {
    if (current.signal.aborted) return
    error.value = errorText(reason)
    state.value = 'error'
  }
}

function restart(): void {
  cursors.value = [undefined]
  page.value = 0
  void load()
}

function move(step: 1 | -1): void {
  if (step === 1) cursors.value = [...cursors.value.slice(0, page.value + 1), nextCursor.value]
  page.value += step
  void load()
}

watch(
  () => [props.scope, props.status, props.search, props.onlyFailed, pageSize.value],
  restart,
  { immediate: true },
)
watch(
  () => props.refreshKey,
  () => void load(),
)
watch(
  () => props.search,
  (value) => {
    searchText.value = value
  },
)
watch(searchText, (value) => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => emit('update:search', value.trim()), 300)
})
onUnmounted(() => {
  controller?.abort()
  window.clearTimeout(searchTimer)
})

function download(): void {
  const query = parameters()
  if (query === null) return
  const link = document.createElement('a')
  link.href = api.usageExportURL(query)
  link.download = ''
  link.click()
}

function toggleColumn(column: Column, checked: boolean): void {
  visible.value = checked
    ? allColumns.filter((item) => item === column || visible.value.includes(item))
    : visible.value.filter((item) => item !== column)
}

function stateDotClass(record: UsageRecord): string {
  if (record.state === 'failed' || record.status >= 400) return 'bg-red-500'
  if (record.state === 'cancelled') return 'bg-gray-500'
  if (record.state === 'tool_calls') return 'bg-blue-500'
  if (record.state === 'completed') return 'bg-green-500'
  return 'bg-yellow-500'
}

function stateTextClass(record: UsageRecord): string {
  if (record.state === 'failed' || record.status >= 400) return 'text-red-400'
  if (record.state === 'cancelled') return 'text-gray-400'
  if (record.state === 'tool_calls') return 'text-blue-400'
  if (record.state === 'completed') return 'text-green-400'
  return 'text-yellow-400'
}

function latencyWidth(milliseconds: number): number {
  return Math.min(100, Math.max(4, (milliseconds / 180_000) * 100))
}
</script>

<template>
  <section
    class="@container min-w-0"
    :class="embedded ? '' : 'rounded-2xl border border-line bg-panel shadow-sm'"
  >
    <header
      class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3"
    >
      <div class="mr-auto flex min-w-0 items-center gap-2.5">
        <span class="rounded-lg border border-line bg-canvas px-2.5 py-1 text-xs font-medium text-gray-300 tabular-nums">
          {{
            !filtered
              ? `${t('usage.records')}: ${integer(total)}`
              : `Rows: ${integer(displayedItems.length)}`
          }}
        </span>
      </div>

      <label v-if="!embedded" class="relative w-full @md:w-auto">
        <span class="sr-only">{{ t('usage.recordsSearch') }}</span>
        <UiIcon
          name="search"
          :size="13"
          class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-gray-500"
        />
        <input
          v-model="searchText"
          type="search"
          :placeholder="t('usage.recordsSearch')"
          class="w-full rounded-lg border border-line bg-canvas py-1.5 pr-2.5 pl-7 text-xs text-white focus:border-blue-500 focus:outline-none @md:w-64"
        />
      </label>

      <UiSelect
        :model-value="status"
        :aria-label="t('usage.dimension.status')"
        class="min-w-32 shrink-0 rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs text-gray-200"
        @update:model-value="emit('update:status', String($event))"
      >
        <option value="">{{ t('usage.recordsAllStatus') }}</option>
        <option v-for="code in statusOptions" :key="code" :value="code">{{ code }}</option>
      </UiSelect>

      <button
        type="button"
        class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition"
        :class="
          masked
            ? 'border-blue-500/40 bg-blue-500/10 text-blue-400'
            : 'border-line bg-canvas text-gray-300 hover:border-line-strong'
        "
        @click="masked = !masked"
      >
        <UiIcon :name="masked ? 'eyeOff' : 'eye'" :size="13" />
        {{ t('usage.masked') }}
      </button>

      <button
        type="button"
        class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition"
        :class="
          onlyFailed
            ? 'border-red-500/40 bg-red-500/10 text-red-400'
            : 'border-line bg-canvas text-gray-300 hover:border-line-strong'
        "
        @click="emit('update:onlyFailed', !onlyFailed)"
      >
        <UiIcon name="warning" :size="13" />
        {{ t('usage.onlyFailed') }}
      </button>

      <PopoverRoot>
        <PopoverTrigger
          class="flex items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs text-gray-300 hover:border-line-strong data-[state=open]:border-blue-500"
        >
          <UiIcon name="columns" :size="13" />
          {{ t('usage.columns') }}
        </PopoverTrigger>
        <PopoverPortal>
          <PopoverContent
            align="end"
            :side-offset="6"
            class="ui-popover z-[60] w-48 rounded-xl border border-line bg-panel p-2 shadow-xl shadow-black/20"
          >
            <fieldset>
              <legend class="sr-only">{{ t('usage.columns') }}</legend>
              <label
                v-for="column in allColumns"
                :key="column"
                :for="`${id}-column-${column}`"
                class="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-xs text-gray-300 hover:bg-raised"
              >
                <input
                  :id="`${id}-column-${column}`"
                  type="checkbox"
                  :checked="visible.includes(column)"
                  @change="toggleColumn(column, ($event.target as HTMLInputElement).checked)"
                />
                {{ columnLabels[column]() }}
              </label>
            </fieldset>
          </PopoverContent>
        </PopoverPortal>
      </PopoverRoot>

      <button
        type="button"
        class="flex items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs text-gray-300 hover:border-line-strong hover:text-white"
        @click="download"
      >
        <UiIcon name="download" :size="13" />
        {{ t('usage.export') }}
      </button>
    </header>

    <div
      v-if="state === 'error'"
      class="flex items-center gap-3 px-4 py-6 text-sm text-red-400"
      role="alert"
    >
      <UiIcon name="warning" :size="16" />
      <span class="flex-1">{{ error }}</span>
      <button
        type="button"
        class="rounded-lg border border-red-500/40 px-3 py-1 text-xs hover:bg-red-500/10"
        @click="load"
      >
        {{ t('usage.retry') }}
      </button>
    </div>
    <div v-else-if="state === 'loading'" class="space-y-2 p-4" aria-hidden="true">
      <div v-for="index in 6" :key="index" class="h-9 animate-pulse rounded-lg bg-raised/60"></div>
    </div>
    <div
      v-else-if="displayedItems.length === 0"
      class="flex flex-col items-center gap-2 px-4 py-12 text-center text-sm text-gray-500"
    >
      <UiIcon name="empty" :size="28" class="text-gray-600" />
      {{ t('usage.recordsEmpty') }}
    </div>
    <template v-else>
      <div class="hidden overflow-x-auto @min-[56rem]:block">
        <table class="w-full text-left text-xs" :aria-labelledby="`${id}-title`">
          <thead class="bg-canvas/50 text-[11px] tracking-wider text-gray-500 uppercase">
            <tr>
              <th
                v-if="visible.includes('account')"
                scope="col"
                class="px-3 py-2.5 font-semibold whitespace-nowrap"
              >
                Source
              </th>
              <th
                v-if="visible.includes('model')"
                scope="col"
                class="px-3 py-2.5 font-semibold whitespace-nowrap"
              >
                {{ t('usage.dimension.model') }}
              </th>
              <th scope="col" class="px-3 py-2.5 font-semibold whitespace-nowrap">
                {{ t('usage.result') }}
              </th>
              <th
                v-if="visible.includes('tokens')"
                scope="col"
                class="px-3 py-2.5 font-semibold whitespace-nowrap"
              >
                Input / Cached
              </th>
              <th
                v-if="visible.includes('output')"
                scope="col"
                class="px-3 py-2.5 text-right font-semibold whitespace-nowrap"
              >
                {{ t('usage.output') }}
              </th>
              <th
                v-if="visible.includes('tps')"
                scope="col"
                class="px-3 py-2.5 text-right font-semibold whitespace-nowrap"
              >
                TPS
              </th>
              <th
                v-if="visible.includes('latency')"
                scope="col"
                class="px-3 py-2.5 font-semibold whitespace-nowrap"
              >
                Elapsed
              </th>
              <th
                v-if="visible.includes('queue')"
                scope="col"
                class="px-3 py-2.5 text-right font-semibold whitespace-nowrap"
                :class="secondaryClass"
              >
                {{ t('usage.queue') }}
              </th>
              <th
                v-if="visible.includes('attempts')"
                scope="col"
                class="px-3 py-2.5 text-right font-semibold whitespace-nowrap"
                :class="secondaryClass"
              >
                {{ t('usage.attempts') }}
              </th>
              <th scope="col" class="px-3 py-2.5 text-right font-semibold whitespace-nowrap">
                {{ t('usage.time') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="record in displayedItems"
              :key="record.id"
              class="cursor-pointer border-t border-line/60 text-gray-300 transition hover:bg-raised/40"
              :class="record.state === 'failed' || record.status >= 400 ? 'bg-red-500/[0.05]' : ''"
              @click="selected = record"
            >
              <td v-if="visible.includes('account')" class="max-w-[13rem] px-3 py-2.5">
                <div v-tooltip="record.account" class="truncate font-semibold text-white">
                  {{ maskAccount(record.account) }}
                </div>
                <div class="mt-0.5 flex items-center gap-1.5 text-[11px] text-gray-500">
                  <span
                    v-if="record.channel"
                    class="rounded border border-line bg-canvas px-1.5 py-0.2"
                    >{{ dimensionLabel('channel', record.channel) }}</span
                  >
                  <span>{{ dimensionLabel('protocol', record.protocol) }}</span>
                </div>
              </td>

              <td v-if="visible.includes('model')" class="max-w-[11rem] px-3 py-2.5">
                <span v-tooltip="record.model" class="block truncate font-mono text-gray-200">{{
                  record.model || '—'
                }}</span>
              </td>

              <td class="px-3 py-2.5 whitespace-nowrap">
                <span class="inline-flex items-center gap-1.5 font-semibold" :class="stateTextClass(record)">
                  <span class="h-1.5 w-1.5 rounded-full" :class="stateDotClass(record)"></span>
                  {{ dimensionLabel('state', record.state) }}
                </span>
                <span class="ml-1.5 font-mono text-[11px] text-gray-500 tabular-nums">{{
                  record.status
                }}</span>
              </td>

              <td v-if="visible.includes('tokens')" class="px-3 py-2.5 whitespace-nowrap tabular-nums">
                <div class="font-semibold text-gray-200">{{ compact(record.input_tokens) }}</div>
                <div
                  class="mt-0.5 inline-flex items-center rounded-full border px-2 py-0.2 text-[11px] font-semibold"
                  :class="
                    record.cached_tokens > 0
                      ? 'border-teal-500/30 bg-teal-500/10 text-cyan-400'
                      : 'border-line bg-canvas text-gray-500'
                  "
                >
                  {{
                    record.cached_tokens > 0
                      ? `Cached ${compact(record.cached_tokens)} (${cacheHitPct(record)})`
                      : 'No Cache (0%)'
                  }}
                </div>
              </td>

              <td
                v-if="visible.includes('output')"
                class="px-3 py-2.5 text-right whitespace-nowrap tabular-nums"
              >
                <div class="font-mono font-medium text-gray-200">
                  {{ integer(record.reasoning_tokens + record.reply_tokens) }}
                </div>
                <div class="text-[11px] text-gray-500">
                  R {{ compact(record.reasoning_tokens) }} / T {{ compact(record.reply_tokens) }}
                </div>
              </td>

              <td
                v-if="visible.includes('tps')"
                class="px-3 py-2.5 text-right font-mono font-semibold text-gray-200 whitespace-nowrap tabular-nums"
              >
                {{ recordTps(record) }}
              </td>

              <td v-if="visible.includes('latency')" class="px-3 py-2.5 whitespace-nowrap">
                <div class="flex items-center gap-2 text-[11px] tabular-nums">
                  <span class="text-gray-500">TTFT</span>
                  <span
                    class="font-semibold"
                    :class="
                      record.first_event_ms <= 0
                        ? 'text-gray-500'
                        : record.first_event_ms > 15_000
                          ? 'text-amber-400'
                          : 'text-green-400'
                    "
                    >{{ record.first_event_ms > 0 ? duration(record.first_event_ms) : '—' }}</span
                  >
                </div>
                <div class="mt-0.5 flex items-center gap-2 text-[11px] text-gray-400 tabular-nums">
                  <span>Total {{ duration(record.duration_ms) }}</span>
                </div>
                <div class="mt-1 h-1 w-24 rounded-full bg-raised">
                  <div
                    class="h-1 rounded-full"
                    :class="latencyTone(record.duration_ms, 'duration')"
                    :style="{ width: `${latencyWidth(record.duration_ms)}%` }"
                  ></div>
                </div>
              </td>

              <td
                v-if="visible.includes('queue')"
                class="px-3 py-2.5 text-right tabular-nums"
                :class="secondaryClass"
              >
                {{ duration(record.queue_ms) }}
              </td>

              <td
                v-if="visible.includes('attempts')"
                class="px-3 py-2.5 text-right tabular-nums"
                :class="secondaryClass"
              >
                <span :class="record.attempts.length > 0 ? 'text-yellow-400' : 'text-gray-600'">{{
                  record.attempts.length
                }}</span>
              </td>

              <td class="px-3 py-2.5 text-right whitespace-nowrap tabular-nums">
                <button
                  type="button"
                  class="font-mono text-gray-300 hover:text-blue-400"
                  :aria-label="`${t('usage.openDetail')} ${record.id}`"
                  @click.stop="selected = record"
                >
                  {{ dateTime(record.time, true) }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="divide-y divide-line/60 @min-[56rem]:hidden">
        <li v-for="record in displayedItems" :key="record.id">
          <button
            type="button"
            class="w-full px-4 py-3 text-left text-xs"
            :class="record.state === 'failed' || record.status >= 400 ? 'bg-red-500/[0.06]' : ''"
            :aria-label="`${t('usage.openDetail')} ${record.id}`"
            @click="selected = record"
          >
            <div class="flex items-center gap-2">
              <span class="inline-flex items-center gap-1 font-semibold" :class="stateTextClass(record)">
                <span class="h-1.5 w-1.5 rounded-full" :class="stateDotClass(record)"></span>
                {{ dimensionLabel('state', record.state) }}
              </span>
              <span class="text-gray-500 tabular-nums">{{ record.status }}</span>
              <span
                v-if="record.cached_tokens > 0"
                class="rounded-full bg-teal-500/10 px-2 py-0.5 text-[10px] font-semibold text-cyan-400"
                >Cache {{ cacheHitPct(record) }}</span
              >
              <span class="ml-auto text-gray-400 tabular-nums">{{
                dateTime(record.time, true)
              }}</span>
            </div>
            <div v-tooltip.overflow="record.model" class="mt-1.5 truncate font-mono text-gray-200">
              {{ record.model || '—' }}
            </div>
            <div class="mt-0.5 flex gap-3 text-gray-500 tabular-nums">
              <span v-tooltip.overflow="record.account" class="truncate">{{
                maskAccount(record.account)
              }}</span>
              <span class="ml-auto shrink-0"
                >{{ t('usage.duration') }} {{ duration(record.duration_ms) }}</span
              >
              <span class="shrink-0"
                >{{ t('usage.input') }} {{ compact(record.input_tokens) }}</span
              >
            </div>
          </button>
        </li>
      </ul>
    </template>

    <footer
      class="flex flex-wrap items-center gap-2 border-t border-line px-4 py-2.5 text-xs text-gray-400"
    >
      <UiSelect
        :model-value="pageSize"
        :aria-label="t('usage.pageSize').replace('{count}', String(pageSize))"
        class="w-32 rounded-lg border border-line bg-canvas px-2 py-1 text-xs text-gray-200"
        @update:model-value="pageSize = Number($event)"
      >
        <option v-for="size in pageSizes" :key="size" :value="size">
          {{ t('usage.pageSize').replace('{count}', String(size)) }}
        </option>
      </UiSelect>
      <div class="ml-auto flex items-center gap-2">
        <span class="tabular-nums">{{
          t('usage.page').replace('{page}', String(page + 1))
        }}</span>
        <button
          type="button"
          class="flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 hover:bg-raised disabled:opacity-40"
          :disabled="page === 0"
          @click="move(-1)"
        >
          <UiIcon name="chevronLeft" :size="12" />{{ t('usage.previousPage') }}
        </button>
        <button
          type="button"
          class="flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 hover:bg-raised disabled:opacity-40"
          :disabled="nextCursor === undefined"
          @click="move(1)"
        >
          {{ t('usage.nextPage') }}<UiIcon name="chevronRight" :size="12" />
        </button>
      </div>
    </footer>
    <RecordDrawer :record="selected" @close="selected = null" />
  </section>
</template>
