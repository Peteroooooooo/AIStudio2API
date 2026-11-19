<script setup lang="ts">
import { computed, ref } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import type { AccountTestOption } from '@/accountTest'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'
import { selectTriggerClass } from './UiSelect.vue'

const props = defineProps<{
  modelValue: string
  options: AccountTestOption[]
  label: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const { t } = useI18n()
const open = ref(false)
const query = ref('')

const selectedOption = computed(() => props.options.find((item) => item.id === props.modelValue))

const showAuto = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (needle === '') return true
  return (
    'auto'.includes(needle) ||
    t('accounts.testModelAuto').toLowerCase().includes(needle) ||
    t('accounts.testModelAutoHint').toLowerCase().includes(needle)
  )
})

const filteredOptions = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (needle === '') return props.options
  return props.options.filter((item) =>
    [item.id, item.name, item.tier ?? '', ...item.aliases].some((field) =>
      field.toLowerCase().includes(needle),
    ),
  )
})

function pick(value: string): void {
  emit('update:modelValue', value)
  open.value = false
}

function tierBadgeClass(tier: 'Pro' | 'Ultra'): string {
  return tier === 'Ultra'
    ? 'border-blue-500/40 bg-blue-500/15 text-blue-300'
    : 'border-amber-700/60 bg-amber-900/20 text-amber-300'
}
</script>

<template>
  <PopoverRoot v-model:open="open" @update:open="(value: boolean) => value && (query = '')">
    <PopoverTrigger
      v-tooltip="label"
      :aria-label="label"
      :class="selectTriggerClass"
      class="max-w-64 rounded-md border border-line bg-canvas px-2.5 py-1.5 text-xs text-gray-200"
    >
      <UiIcon name="throughput" :size="12" class="text-gray-400" />
      <span class="min-w-0 flex-1 truncate">{{
        props.modelValue === ''
          ? t('accounts.testModelAuto')
          : (selectedOption?.name ?? props.modelValue)
      }}</span>
      <span
        v-if="selectedOption?.tier"
        class="shrink-0 rounded border px-1 py-0 text-[10px] leading-4 font-medium"
        :class="tierBadgeClass(selectedOption.tier)"
      >
        {{ selectedOption.tier }}
      </span>
      <UiIcon
        name="chevronDown"
        :size="12"
        class="text-gray-500 transition-transform duration-150 group-data-[state=open]:rotate-180"
      />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="start"
        :side-offset="6"
        :collision-padding="12"
        class="ui-popover z-[80] w-80 max-w-[var(--reka-popover-content-available-width)] min-w-[var(--reka-popover-trigger-width)] rounded-lg border border-line bg-panel p-2 shadow-xl shadow-black/40 sm:w-96"
      >
        <label class="relative mb-2 block">
          <span class="sr-only">{{ t('settings.modelSearch') }}</span>
          <UiIcon
            name="search"
            :size="13"
            class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-gray-500"
          />
          <input
            v-model="query"
            type="search"
            :placeholder="t('settings.modelSearch')"
            class="w-full rounded-md border border-line bg-canvas py-1.5 pr-2 pl-7 text-xs text-white focus:border-blue-500 focus:outline-none"
          />
        </label>
        <div
          role="listbox"
          :aria-label="label"
          class="max-h-[min(22rem,var(--reka-popover-content-available-height))] min-w-0 space-y-0.5 overflow-y-auto [scrollbar-gutter:stable]"
        >
          <button
            v-if="showAuto"
            type="button"
            role="option"
            :aria-selected="props.modelValue === ''"
            class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left transition hover:bg-raised"
            :class="props.modelValue === '' ? 'bg-blue-600/10 text-blue-300' : 'text-gray-200'"
            @click="pick('')"
          >
            <div class="min-w-0 flex-1">
              <div class="truncate text-xs font-medium">{{ t('accounts.testModelAuto') }}</div>
              <div class="truncate text-[11px] text-gray-500">
                {{ t('accounts.testModelAutoHint') }}
              </div>
            </div>
            <UiIcon v-if="props.modelValue === ''" name="check" :size="13" class="text-blue-400" />
          </button>
          <div
            v-if="showAuto && filteredOptions.length > 0"
            class="my-1 border-t border-line/60"
          ></div>
          <p
            v-if="!showAuto && filteredOptions.length === 0"
            class="px-2 py-4 text-center text-xs text-gray-500"
          >
            {{ t('settings.modelNone') }}
          </p>
          <button
            v-for="option in filteredOptions"
            :key="option.id"
            type="button"
            role="option"
            :aria-selected="props.modelValue === option.id"
            class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left transition hover:bg-raised"
            :class="
              props.modelValue === option.id ? 'bg-blue-600/10 text-blue-300' : 'text-gray-200'
            "
            @click="pick(option.id)"
          >
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5">
                <span v-tooltip.overflow="option.name" class="truncate text-xs font-medium">{{
                  option.name
                }}</span>
                <span
                  v-if="option.tier"
                  class="shrink-0 rounded border px-1 py-0 text-[10px] leading-4 font-medium"
                  :class="tierBadgeClass(option.tier)"
                >
                  {{ option.tier }}
                </span>
                <span
                  v-if="option.pinned"
                  class="shrink-0 rounded border border-line bg-raised px-1 py-0 text-[10px] leading-4 text-gray-400"
                >
                  {{ t('accounts.testModelPinned') }}
                </span>
              </div>
              <div
                v-if="option.name !== option.id"
                v-tooltip.overflow="option.id"
                class="truncate font-mono text-[11px] text-gray-500"
              >
                {{ option.id }}
              </div>
            </div>
            <UiIcon
              v-if="props.modelValue === option.id"
              name="check"
              :size="13"
              class="text-blue-400"
            />
          </button>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
