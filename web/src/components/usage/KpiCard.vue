<script setup lang="ts">
import { computed } from 'vue'
import type { IconName } from '@/icons'
import UiIcon from '../UiIcon.vue'

const props = defineProps<{
  label: string
  icon: IconName
  value: string
  raw: number | null
  help: string
  details: { label: string; value: string; tone?: string | undefined }[]
  delta?:
    | { text: string; tone: 'good' | 'bad' | 'neutral'; direction: 'up' | 'down'; help: string }
    | undefined
  spark: number[]
  color: string
  tone?: string | undefined
  iconBg?: string | undefined
  highlight?: boolean | undefined
}>()

const sparkPath = computed(() => {
  const values = props.spark
  if (values.length < 2) return ''
  const high = Math.max(...values)
  const low = Math.min(...values)
  const span = high - low || 1
  return values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * 100
      const y = 26 - ((value - low) / span) * 22
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
})

const deltaClass = computed(() => {
  if (props.delta === undefined) return ''
  return { good: 'text-green-400', bad: 'text-red-400', neutral: 'text-gray-500' }[props.delta.tone]
})
</script>

<template>
  <article
    class="group relative flex min-w-0 flex-col overflow-hidden rounded-2xl border p-4 shadow-sm transition hover:-translate-y-0.5 hover:shadow-md"
    :class="
      highlight
        ? 'border-teal-400/50 bg-gradient-to-b from-panel to-teal-500/[0.06]'
        : 'border-line bg-panel'
    "
  >
    <div class="flex items-center gap-2 text-xs text-gray-500">
      <span
        class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg"
        :class="iconBg ?? 'bg-blue-500/10 text-blue-400'"
      >
        <UiIcon :name="icon" :size="14" />
      </span>
      <span
        v-tooltip="help"
        class="min-w-0 truncate cursor-help font-medium"
        :class="highlight ? 'text-cyan-400' : 'text-gray-400'"
        >{{ label }}</span
      >
      <span
        v-if="delta"
        v-tooltip="delta.help"
        class="ml-auto flex shrink-0 items-center text-[11px] font-medium tabular-nums"
        :class="deltaClass"
      >
        <UiIcon :name="delta.direction" :size="11" />{{ delta.text }}
        <span class="sr-only">{{ delta.help }}</span>
      </span>
    </div>

    <data
      v-tooltip.overflow="value"
      :value="raw ?? ''"
      class="relative z-10 mt-2.5 block truncate text-[26px] leading-tight font-bold tracking-tight tabular-nums"
      :class="tone ?? 'text-white'"
      >{{ value }}</data
    >

    <p class="relative z-10 mt-1.5 text-xs text-gray-500">
      <template v-for="(detail, index) in details" :key="detail.label">
        <span v-if="index > 0" aria-hidden="true"> · </span>{{ detail.label }}
        <span
          v-if="detail.value"
          class="font-medium tabular-nums"
          :class="detail.tone ?? 'text-gray-300'"
          >{{ detail.value }}</span
        >
      </template>
    </p>

    <svg
      v-if="sparkPath"
      class="pointer-events-none absolute right-2.5 bottom-2 h-9 w-28 opacity-75"
      viewBox="0 0 100 28"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <path
        :d="sparkPath"
        fill="none"
        :stroke="color"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  </article>
</template>
