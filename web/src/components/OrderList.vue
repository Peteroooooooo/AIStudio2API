<script setup lang="ts">
import { computed, ref } from 'vue'
import FilterMenu from './FilterMenu.vue'
import UiIcon from './UiIcon.vue'

// OrderList 是可从菜单勾选、拖动或上下移动排序的有序列表，取值为逗号分隔的 ID
const props = defineProps<{
  options: string[]
  value: string
  format: (id: string) => string
  labelledby: string
  // large 表示勾选菜单使用更大的弹出面板
  large?: boolean
  text: {
    drag: (label: string) => string
    up: (label: string) => string
    down: (label: string) => string
    remove: (label: string) => string
    add: string
    search: string
    empty: string
    clear: string
  }
}>()
const emit = defineEmits<{ change: [value: string] }>()

// dragging 与 over 是拖动中的条目序号和指针所在的条目序号
const dragging = ref(-1)
const over = ref(-1)

// selected 是取值中列出且仍在可选项里的 ID，按取值顺序
const selected = computed(() => {
  const ids = new Set(props.options)
  return [
    ...new Set(
      props.value
        .split(',')
        .map((id) => id.trim())
        .filter((id) => ids.has(id)),
    ),
  ]
})

// write 写回顺序
function write(ids: string[]): void {
  emit('change', ids.join(','))
}

// select 按菜单的选择更新列表，已列出的保持原位，新选的追加到末尾
function select(ids: string[]): void {
  write([
    ...selected.value.filter((id) => ids.includes(id)),
    ...ids.filter((id) => !selected.value.includes(id)),
  ])
}

// move 把序号 from 的条目移到序号 to
function move(from: number, to: number): void {
  const ids = [...selected.value]
  ids.splice(to, 0, ...ids.splice(from, 1))
  write(ids)
}

// drop 把拖动中的条目放到指针所在的位置
function drop(index: number): void {
  if (dragging.value !== -1 && dragging.value !== index) move(dragging.value, index)
  endDrag()
}

// endDrag 结束拖动
function endDrag(): void {
  dragging.value = -1
  over.value = -1
}

const actionClass =
  'rounded p-0.5 text-gray-500 opacity-0 transition group-hover/item:opacity-100 hover:text-gray-200 focus-visible:opacity-100 [@media(hover:none)]:opacity-100'
</script>

<template>
  <div class="space-y-2">
    <ol v-if="selected.length > 0" :aria-labelledby="labelledby">
      <li
        v-for="(id, index) in selected"
        :key="id"
        class="group/item flex items-center gap-1.5 rounded border-t py-1 pr-1 transition-colors hover:bg-raised"
        :class="over === index && dragging !== index ? 'border-gray-400' : 'border-transparent'"
        @dragover.prevent="over = index"
        @dragleave="over = -1"
        @drop.prevent="drop(index)"
      >
        <span
          draggable="true"
          class="cursor-grab text-gray-600 opacity-0 group-hover/item:opacity-100 hover:text-gray-300 active:cursor-grabbing [@media(hover:none)]:opacity-100"
          :aria-label="text.drag(format(id))"
          @dragstart="dragging = index"
          @dragend="endDrag"
        >
          <UiIcon name="grip" :size="14" />
        </span>
        <span class="w-4 shrink-0 text-right text-xs text-gray-500 tabular-nums">{{
          index + 1
        }}</span>
        <span
          v-tooltip.overflow="format(id)"
          class="min-w-0 flex-1 truncate text-sm text-gray-200"
          >{{ format(id) }}</span
        >
        <button
          type="button"
          :class="[actionClass, 'disabled:!opacity-0']"
          :aria-label="text.up(format(id))"
          :disabled="index === 0"
          @click="move(index, index - 1)"
        >
          <UiIcon name="chevronUp" :size="14" />
        </button>
        <button
          type="button"
          :class="[actionClass, 'disabled:!opacity-0']"
          :aria-label="text.down(format(id))"
          :disabled="index === selected.length - 1"
          @click="move(index, index + 1)"
        >
          <UiIcon name="chevronDown" :size="14" />
        </button>
        <button
          type="button"
          :class="actionClass"
          :aria-label="text.remove(format(id))"
          @click="write(selected.filter((item) => item !== id))"
        >
          <UiIcon name="close" :size="14" />
        </button>
      </li>
    </ol>
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <FilterMenu
        field
        :large="large"
        class="w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white"
        :label="text.add"
        :options="options"
        :selected="selected"
        :format="format"
        :search-label="text.search"
        :empty-label="text.empty"
        :clear-label="text.clear"
        @change="select"
      />
      <slot :selected="selected" />
    </div>
  </div>
</template>
