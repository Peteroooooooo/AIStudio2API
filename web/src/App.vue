<script setup lang="ts">
defineProps<{ adminUsername?: string }>()
const emit = defineEmits<{ logout: [] }>()
import { computed, defineAsyncComponent, h, onMounted, ref, useTemplateRef, watch } from 'vue'
import type { AsyncComponentLoader } from 'vue'
import {
  DialogContent,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { waitsForCamoufox } from '@/camoufox'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { TabID } from '@/types'
import CamoufoxBanner from '@/components/CamoufoxBanner.vue'
import UiDisclosure from '@/components/UiDisclosure.vue'
import LogsPanel from '@/components/LogsPanel.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import UiIcon from '@/components/UiIcon.vue'
import { useClampToggle } from '@/clamp'
import { afterTask } from '@/lazy'
import { useTabParam } from '@/url'
import { useRoute, useRouter } from 'vue-router'
import type { IconName } from '@/icons'

const { availableLocales, locale, setLocale, t, logText } = useI18n()

function lazyPanel(loader: AsyncComponentLoader) {
  return defineAsyncComponent({
    loader: () => loader().then(afterTask),
    delay: 150,
    loadingComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-gray-500', role: 'status' }, t('app.panelLoading')),
    },
    errorComponent: {
      render: () =>
        h('p', { class: 'p-8 text-sm text-red-300', role: 'alert' }, t('app.panelFailed')),
    },
  })
}

const UsagePanel = lazyPanel(() => import('@/components/UsagePanel.vue'))
const VersionPanel = lazyPanel(() => import('@/components/VersionPanel.vue'))
const AccessPanel = lazyPanel(() => import('@/components/AccessPanel.vue'))
const AccountsPanel = lazyPanel(() => import('@/components/AccountsPanel.vue'))
const ModelsTable = lazyPanel(() => import('@/components/ModelsTable.vue'))
const SettingsPanel = lazyPanel(() => import('@/components/SettingsPanel.vue'))
const StudioLayout = defineAsyncComponent(() =>
  import('@/playground/StudioLayout.vue').then(afterTask),
)

function preloadStudio(): void {
  void import('@/playground/StudioLayout.vue')
  void import('@/playground/PlaygroundPage.vue')
}

const validTabs: TabID[] = [
  'usage',
  'accounts',
  'models',
  'access',
  'settings',
  'requests',
  'logs',
  'version',
]
const { tab: currentTab, select: selectTab } = useTabParam(validTabs, 'usage')
const router = useRouter()
const route = useRoute()
const isStudio = computed(() => Boolean(route.meta.studio))

const logsDrawerOpen = ref(false)
const versionModalOpen = ref(false)

type TopNavID = 'usage' | 'accounts' | 'models' | 'playground' | 'history' | 'settings'

const activeNav = computed<TopNavID>(() => {
  if (isStudio.value) return route.name === 'library' ? 'history' : 'playground'
  if (currentTab.value === 'access' || currentTab.value === 'models') return 'models'
  if (currentTab.value === 'accounts') return 'accounts'
  if (currentTab.value === 'settings') return 'settings'
  return 'usage'
})

watch(
  () => route.fullPath,
  () => {
    if (route.name === 'console') {
      const tab = route.query.tab as TabID
      if (tab === 'logs') {
        logsDrawerOpen.value = true
        currentTab.value = 'usage'
      } else if (tab === 'version') {
        versionModalOpen.value = true
        currentTab.value = 'usage'
      } else {
        currentTab.value = validTabs.includes(tab) ? tab : 'usage'
      }
    }
  },
  { immediate: true },
)

function openTab(tab: TabID): void {
  if (tab === 'playground') {
    void router.push('/prompts/new_chat')
  } else if (tab === 'history') {
    void router.push('/library')
  } else if (isStudio.value) {
    currentTab.value = tab
    void router.push(tab === 'usage' ? '/' : { path: '/', query: { tab } })
  } else {
    selectTab(tab)
  }
}

const {
  status,
  logs,
  accounts,
  accountTasks,
  visibleAccounts,
  models,
  cooldowns,
  requests,
  config,
  update,
  loading,
  errors,
  notice,
  pendingDelete,
  serviceState,
  startPending,
  stopPending,
  showNotice,
  closeNotice,
  loadAccountData,
  loadRequestData,
  updateAccount,
  deleteAccounts,
  undoDelete,
  commitDelete,
  startService,
  stopService,
  retryCamoufox,
  clearLogs,
  waitForRestart,
} = useConsole()

const noticeText = computed(() => logText(notice.message))
const deletedNotice = computed(() => {
  const list = pendingDelete.value?.accounts ?? []
  return list.length === 1
    ? t('accounts.deletedNamed').replace('{name}', list[0]!.label)
    : t('accounts.deletedCount').replace('{count}', String(list.length))
})
const noticeBody = useTemplateRef<HTMLElement>('noticeBody')
const noticeOpen = ref(false)
const noticeClamped = useClampToggle(noticeBody, noticeOpen, 3)
const noticeClipped = ref(false)
watch(
  noticeText,
  () => {
    noticeOpen.value = false
    noticeClipped.value =
      noticeBody.value !== null && noticeBody.value.scrollHeight > noticeBody.value.clientHeight
  },
  { flush: 'post' },
)

const navigation: { id: TopNavID; tab: TabID; label: TranslationKey; icon: IconName }[] = [
  { id: 'usage', tab: 'usage', label: 'nav.usage', icon: 'usage' },
  { id: 'accounts', tab: 'accounts', label: 'nav.accounts', icon: 'accounts' },
  { id: 'models', tab: 'models', label: 'nav.modelsAccess', icon: 'models' },
  { id: 'playground', tab: 'playground', label: 'nav.playground', icon: 'playground' },
  { id: 'history', tab: 'history', label: 'pg.history', icon: 'history' },
  { id: 'settings', tab: 'settings', label: 'nav.settings', icon: 'settings' },
]

const camoufoxInTask = computed(
  () =>
    !isStudio.value &&
    currentTab.value === 'accounts' &&
    accountTasks.value.some((task) => waitsForCamoufox(task, status.value?.camoufox)),
)

const statusDotClass = computed(() => {
  if (serviceState.value === 'running') return 'bg-green-500'
  if (serviceState.value === 'launching') return 'bg-cyan-400 animate-pulse'
  return 'bg-gray-500'
})

const statusChipClass = computed(() => {
  if (serviceState.value === 'running') return 'border-green-500/30 bg-green-500/10 text-green-400'
  if (serviceState.value === 'launching') return 'border-cyan-500/30 bg-cyan-500/10 text-cyan-300'
  return 'border-line bg-canvas text-gray-400'
})

const serviceStateLabel = computed(() => t(`state.${serviceState.value}`))

async function logout(): Promise<void> {
  await commitDelete()
  emit('logout')
}

onMounted(() => {
  if (typeof window.requestIdleCallback === 'function') window.requestIdleCallback(preloadStudio)
  else globalThis.setTimeout(preloadStudio, 400)
})
</script>

<template>
  <div class="flex h-full w-full flex-col bg-canvas">
    <!-- Top Compact CPA-Style Navigation Bar -->
    <header class="shrink-0 border-b border-line bg-panel/95 backdrop-blur">
      <div class="mx-auto flex min-h-13 w-full max-w-[90rem] flex-wrap items-center justify-between gap-2.5 px-4 py-2 md:px-6">
        <!-- Brand & Version Badge -->
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="flex items-center gap-2 text-left"
            @click="openTab('usage')"
          >
            <img src="/favicon.svg" alt="AIStudio2API" class="h-7 w-7 shrink-0" />
            <span class="text-sm font-bold tracking-tight text-white">AIStudio2API</span>
          </button>
          <button
            v-if="!loading.status"
            type="button"
            class="relative inline-flex items-center gap-1.5 rounded-full border border-line bg-canvas px-2 py-0.5 font-mono text-[11px] text-gray-400 transition hover:border-line-strong hover:text-white"
            :title="t('nav.version')"
            @click="versionModalOpen = true"
          >
            <span class="max-w-[7.5rem] truncate">{{ status?.version || 'v—' }}</span>
            <span
              v-if="update?.available"
              class="h-2 w-2 shrink-0 rounded-full bg-amber-400"
              :title="t('version.available').replace('{version}', update.latest!)"
            ></span>
          </button>
        </div>

        <!-- 6 Converged Navigation Pills -->
        <nav
          class="flex max-w-full items-center gap-0.5 overflow-x-auto rounded-xl border border-line bg-canvas p-1"
        >
          <button
            v-for="item in navigation"
            :key="item.id"
            type="button"
            class="inline-flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium whitespace-nowrap transition"
            :class="
              activeNav === item.id
                ? 'bg-panel text-blue-400 shadow-sm font-semibold'
                : 'text-gray-400 hover:bg-raised/60 hover:text-white'
            "
            :aria-current="activeNav === item.id ? 'page' : undefined"
            @pointerenter="(item.id === 'playground' || item.id === 'history') && preloadStudio()"
            @click="openTab(item.tab)"
          >
            <UiIcon :name="item.icon" :size="14" class="shrink-0" />
            <span>{{ t(item.label) }}</span>
            <span
              v-if="item.id === 'accounts' && visibleAccounts.length > 0"
              class="rounded-full bg-blue-500/12 px-1.5 py-0.2 text-[10px] font-semibold text-blue-400 tabular-nums"
            >
              {{ visibleAccounts.length }}
            </span>
          </button>
        </nav>

        <!-- Right Service Controls & Utilities -->
        <div class="flex items-center gap-1.5">
          <div
            class="inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-semibold"
            :class="statusChipClass"
          >
            <span class="h-2 w-2 rounded-full" :class="statusDotClass"></span>
            <span class="hidden sm:inline">{{ serviceStateLabel }}</span>
            <button
              v-if="serviceState === 'stopped' || serviceState === 'unavailable'"
              type="button"
              class="inline-flex items-center gap-1 rounded-full bg-green-600 px-2 py-0.5 text-[11px] font-semibold text-white hover:bg-green-500 disabled:opacity-50"
              :disabled="startPending || stopPending"
              @click="startService"
            >
              <UiIcon :name="startPending ? 'spinner' : 'play'" :size="11" />
              {{ t('app.start') }}
            </button>
            <button
              v-else-if="serviceState === 'launching' || serviceState === 'running'"
              type="button"
              class="inline-flex items-center gap-1 rounded-full border border-red-500/30 bg-red-500/12 px-2 py-0.5 text-[11px] font-semibold text-red-400 hover:bg-red-500/20 disabled:opacity-50"
              :disabled="stopPending"
              @click="stopService"
            >
              <UiIcon :name="stopPending ? 'spinner' : 'stop'" :size="10" />
              {{ t('app.stop') }}
            </button>
          </div>

          <button
            type="button"
            class="inline-flex h-7.5 items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 text-xs font-medium text-gray-300 transition hover:border-line-strong hover:text-white"
            @click="logsDrawerOpen = true"
          >
            <UiIcon name="logs" :size="13" />
            <span class="hidden md:inline">{{ t('nav.logs') }}</span>
          </button>

          <ThemeToggle />

          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip="t('app.language')"
              :aria-label="t('app.language')"
              class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border border-line text-gray-400 transition hover:border-line-strong hover:text-white"
            >
              <UiIcon name="language" :size="13" />
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="end"
                :side-offset="6"
                class="z-[60] max-h-[var(--reka-dropdown-menu-content-available-height)] overflow-y-auto rounded-xl border border-line bg-panel p-1 shadow-xl"
              >
                <DropdownMenuItem
                  v-for="item in availableLocales"
                  :key="item.code"
                  class="flex w-full cursor-pointer items-center justify-between gap-4 rounded-lg px-3 py-1.5 text-left text-xs whitespace-nowrap text-gray-300 outline-none data-[highlighted]:bg-blue-600 data-[highlighted]:text-white"
                  @select="setLocale(item.code)"
                >
                  {{ item.label }}
                  <UiIcon v-if="item.code === locale" name="check" :size="12" />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>

          <button
            v-if="adminUsername"
            v-tooltip="`${adminUsername} · ${t('auth.logout')}`"
            type="button"
            :aria-label="`${adminUsername} · ${t('auth.logout')}`"
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border border-line text-gray-400 transition hover:border-line-strong hover:text-white"
            @click="logout"
          >
            <UiIcon name="login" :size="13" />
          </button>
        </div>
      </div>
    </header>

    <main class="@container flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      <div
        v-if="config?.reserved_listen_addr"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2 text-xs break-words text-amber-300"
      >
        {{
          t('app.listenReserved')
            .replace('{reserved}', config.reserved_listen_addr)
            .replace('{active}', config.active_listen_addr)
        }}
      </div>
      <CamoufoxBanner
        v-if="status && !camoufoxInTask"
        :camoufox="status.camoufox"
        @retry="retryCamoufox"
      />
      <div
        v-if="config?.invalid.length"
        role="alert"
        class="page-x overflow-hidden border-b border-amber-500/30 bg-amber-500/10 py-2 text-xs break-words text-amber-300"
      >
        <p>{{ t('app.invalidConfig') }}</p>
        <p v-for="item in config.invalid" :key="item.key" class="mt-1 font-mono text-xs">
          {{ item.value ? `${item.key}=${item.value}` : item.key }} · {{ logText(item.reason) }}
        </p>
      </div>

      <!-- Models & Access Sub-Switch Banner -->
      <div
        v-if="!isStudio && (currentTab === 'models' || currentTab === 'access')"
        class="page-x flex items-center gap-2 border-b border-line bg-panel/60 py-2"
      >
        <div class="inline-flex items-center gap-1 rounded-xl border border-line bg-canvas p-1">
          <button
            type="button"
            class="rounded-lg px-3 py-1 text-xs font-medium transition"
            :class="
              currentTab === 'models'
                ? 'bg-panel text-blue-400 shadow-sm font-semibold'
                : 'text-gray-400 hover:text-white'
            "
            @click="openTab('models')"
          >
            {{ t('nav.models') }}
          </button>
          <button
            type="button"
            class="rounded-lg px-3 py-1 text-xs font-medium transition"
            :class="
              currentTab === 'access'
                ? 'bg-panel text-blue-400 shadow-sm font-semibold'
                : 'text-gray-400 hover:text-white'
            "
            @click="openTab('access')"
          >
            {{ t('nav.access') }}
          </button>
        </div>
      </div>

      <Transition name="panel-fade" mode="out-in">
        <KeepAlive
          include="AccountsPanel,AccessPanel,ModelsTable,SettingsPanel,UsagePanel,StudioLayout"
        >
          <StudioLayout v-if="isStudio" />
          <AccountsPanel
            v-else-if="currentTab === 'accounts'"
            :accounts="visibleAccounts"
            :tasks="accountTasks"
            :models="models"
            :loading="loading.accounts"
            :error="errors.accounts"
            :chrome-import="status?.chrome_import ?? false"
            :camoufox="status?.camoufox"
            :geoip="status?.geoip"
            :test-models="config?.account_test_models ?? ''"
            @refresh="loadAccountData"
            @updated="updateAccount"
            @notice="showNotice"
            @delete="deleteAccounts"
          />
          <AccessPanel
            v-else-if="currentTab === 'access'"
            :config="config"
            :models="models"
            @settings="openTab('settings')"
          />
          <ModelsTable
            v-else-if="currentTab === 'models'"
            :models="models"
            :loading="loading.models"
            :error="errors.models"
            :config="config"
            :accounts="accounts"
            @saved="config = $event"
            @notice="showNotice"
          />
          <SettingsPanel
            v-else-if="currentTab === 'settings'"
            :config="config"
            :accounts="accounts"
            :models="models"
            :loading="loading.config || loading.accounts"
            :error="errors.config"
            @saved="config = $event"
            @notice="showNotice"
          />
          <UsagePanel
            v-else
            :models="models"
            :accounts="accounts"
            :cooldowns="cooldowns"
            :requests="requests"
            :requests-loading="loading.requests || loading.cooldowns"
            :cooldown-error="errors.cooldowns"
            :request-error="errors.requests"
            :initial-sub-tab="currentTab === 'requests' ? 'cooldowns' : 'realtime'"
            @refresh-requests="loadRequestData"
            @notice="showNotice"
          />
        </KeepAlive>
      </Transition>
    </main>

    <!-- System Console Logs Slide-Over Drawer -->
    <DialogRoot v-model:open="logsDrawerOpen">
      <DialogPortal>
        <DialogOverlay class="fixed inset-0 z-[70] bg-black/50 backdrop-blur-[1px]" />
        <DialogContent
          class="fixed top-0 right-0 z-[71] flex h-full w-[min(64rem,96vw)] flex-col border-l border-line bg-canvas shadow-2xl focus:outline-none"
        >
          <div class="flex items-center justify-between border-b border-line bg-panel px-4 py-3">
            <DialogTitle class="text-sm font-semibold text-white">{{ t('nav.logs') }}</DialogTitle>
            <button
              type="button"
              class="rounded-lg border border-line px-2.5 py-1 text-xs text-gray-300 hover:bg-raised hover:text-white"
              @click="logsDrawerOpen = false"
            >
              {{ t('common.close') }}
            </button>
          </div>
          <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
            <LogsPanel :logs="logs" @clear="clearLogs" />
          </div>
        </DialogContent>
      </DialogPortal>
    </DialogRoot>

    <!-- Version & Update Modal -->
    <DialogRoot v-model:open="versionModalOpen">
      <DialogPortal>
        <DialogOverlay class="fixed inset-0 z-[70] bg-black/50 backdrop-blur-[1px]" />
        <DialogContent
          class="fixed top-1/2 left-1/2 z-[71] flex max-h-[85vh] w-[min(46rem,94vw)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-line bg-canvas shadow-2xl focus:outline-none"
        >
          <div class="flex items-center justify-between border-b border-line bg-panel px-4 py-3">
            <DialogTitle class="text-sm font-semibold text-white">{{ t('nav.version') }}</DialogTitle>
            <button
              type="button"
              class="rounded-lg border border-line px-2.5 py-1 text-xs text-gray-300 hover:bg-raised hover:text-white"
              @click="versionModalOpen = false"
            >
              {{ t('common.close') }}
            </button>
          </div>
          <div class="flex min-h-0 flex-1 flex-col overflow-auto">
            <VersionPanel
              :status="update"
              :active-requests="status?.active_requests ?? 0"
              @status="update = $event"
              @restarting="waitForRestart"
            />
          </div>
        </DialogContent>
      </DialogPortal>
    </DialogRoot>

    <!-- Notice & Undo Delete Toasts -->
    <div
      class="pointer-events-none fixed right-5 bottom-5 left-5 z-50 flex flex-col items-end gap-2 sm:left-auto sm:max-w-md"
    >
      <Transition name="notice">
        <div
          v-if="notice.message"
          role="status"
          class="pointer-events-auto flex max-w-full items-start gap-2 rounded-xl border bg-panel py-3 pr-2 pl-4 text-sm shadow-xl"
          :class="
            notice.tone === 'error'
              ? 'border-red-500/50 text-red-300'
              : 'border-green-500/50 text-green-300'
          "
        >
          <UiDisclosure v-if="noticeClipped" v-model="noticeOpen" />
          <p
            ref="noticeBody"
            class="min-w-0 flex-1 break-words"
            :class="noticeClamped ? 'line-clamp-3' : 'max-h-[40vh] overflow-y-auto'"
          >
            {{ noticeText }}
          </p>
          <button
            type="button"
            class="-my-1 shrink-0 rounded p-1 text-gray-500 transition hover:bg-raised hover:text-gray-200"
            :aria-label="t('common.close')"
            @click="closeNotice"
          >
            <UiIcon name="close" :size="14" />
          </button>
        </div>
      </Transition>
      <Transition name="notice">
        <div
          v-if="pendingDelete"
          class="pointer-events-auto flex max-w-full items-center gap-4 rounded-xl border border-line-strong bg-panel px-4 py-3 text-sm text-gray-200 shadow-xl"
          role="status"
        >
          <span v-tooltip.overflow="deletedNotice" class="min-w-0 truncate">{{
            deletedNotice
          }}</span>
          <button
            type="button"
            class="flex shrink-0 items-center gap-1.5 rounded px-2 py-1 font-medium text-blue-400 hover:bg-raised"
            @click="undoDelete"
          >
            <UiIcon name="undo" :size="14" />
            {{ t('common.undo') }}
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.panel-fade-enter-active {
  transition: opacity 140ms ease-out;
}

.panel-fade-leave-active {
  transition: opacity 80ms ease-in;
}

.panel-fade-enter-from,
.panel-fade-leave-to {
  opacity: 0;
}
</style>
