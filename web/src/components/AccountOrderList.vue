<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import type { Account } from '@/types'
import OrderList from './OrderList.vue'

const props = defineProps<{ accounts: Account[]; value: string }>()
const emit = defineEmits<{ change: [value: string] }>()

const { t } = useI18n()

// label 返回账户 ID 对应的显示名称
function label(id: string): string {
  return props.accounts.find((account) => account.id === id)?.label ?? id
}

const text = computed(() => ({
  drag: (name: string) => t('settings.accountOrderDrag').replace('{account}', name),
  up: (name: string) => t('settings.accountOrderUp').replace('{account}', name),
  down: (name: string) => t('settings.accountOrderDown').replace('{account}', name),
  remove: (name: string) => t('settings.accountOrderRemove').replace('{account}', name),
  add: t('settings.accountOrderAdd'),
  search: t('settings.accountSearch'),
  empty: t('settings.accountNone'),
  clear: t('settings.accountOrderClear'),
}))
</script>

<template>
  <OrderList
    v-slot="{ selected }"
    :options="accounts.map((account) => account.id)"
    :value="value"
    :format="label"
    labelledby="setting-account-order-label"
    :text="text"
    @change="emit('change', $event)"
  >
    <span class="text-xs text-gray-500">{{
      selected.length === 0
        ? t('settings.accountOrderUnset')
        : t('settings.accountOrderRest').replace(
            '{count}',
            String(accounts.length - selected.length),
          )
    }}</span>
  </OrderList>
</template>
