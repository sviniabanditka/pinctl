import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import HomeView from './HomeView.vue'
import DeviceFormDialog from '../components/DeviceFormDialog.vue'
import { useDevicesStore } from '../stores/devices'
import { useBoardsStore } from '../stores/boards'
import { usePipelinesStore } from '../stores/pipelines'
import { useToasts } from '../composables/toast'
import { ApiError } from '../api/client'
import StatusBadge from '../components/StatusBadge.vue'
import type { Device, PipelineSummary } from '../api/types'

function device(id: string, role: Device['role'], pin: number): Device {
  return {
    id,
    name: `dev ${id}`,
    boardId: 'brd_1',
    pin,
    role,
    options: {},
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }
}

async function mountHome() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: HomeView }] })
  router.push('/')
  await router.isReady()

  const boards = useBoardsStore()
  boards.loaded = true
  const devices = useDevicesStore()
  devices.loaded = true
  const pipelines = usePipelinesStore()
  pipelines.loaded = true

  const wrapper = mount(HomeView, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, devices, boards, pipelines }
}

describe('HomeView: devices control mapping', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('gives digital-out a toggle switch', async () => {
    const { wrapper, devices } = await mountHome()
    devices.devices = [device('dev_do', 'digital-out', 5)]
    await wrapper.vm.$nextTick()

    const row = wrapper.find('[data-label="Control"]')
    expect(row.find('.toggle-switch').exists()).toBe(true)
    expect(row.find('input[type="range"]').exists()).toBe(false)
  })

  it('gives pwm-out a 0-255 slider', async () => {
    const { wrapper, devices } = await mountHome()
    devices.devices = [device('dev_pwm', 'pwm-out', 6)]
    await wrapper.vm.$nextTick()

    const slider = wrapper.find('[data-label="Control"] input[type="range"]')
    expect(slider.exists()).toBe(true)
    expect(slider.attributes('max')).toBe('255')
  })

  it('gives servo a 0-180 slider with a degree suffix', async () => {
    const { wrapper, devices } = await mountHome()
    devices.devices = [device('dev_servo', 'servo', 7)]
    await wrapper.vm.$nextTick()

    const control = wrapper.find('[data-label="Control"]')
    const slider = control.find('input[type="range"]')
    expect(slider.exists()).toBe(true)
    expect(slider.attributes('min')).toBe('0')
    expect(slider.attributes('max')).toBe('180')
    expect(control.text()).toContain('°')
  })

  it('gives input roles no control', async () => {
    const { wrapper, devices } = await mountHome()
    devices.devices = [device('dev_in', 'analog-in', 0)]
    await wrapper.vm.$nextTick()

    const control = wrapper.find('[data-label="Control"]')
    expect(control.find('.toggle-switch').exists()).toBe(false)
    expect(control.find('input[type="range"]').exists()).toBe(false)
    expect(control.text().trim()).toBe('—')
  })
})

describe('HomeView: unsupported_pin_mode surfaces as a toast', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useToasts().toasts.splice(0)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders a toast naming the pin/role/supported-pins detail from the server', async () => {
    const { wrapper, devices } = await mountHome()
    const createSpy = vi.spyOn(devices, 'create').mockRejectedValue(
      new ApiError(400, {
        code: 'unsupported_pin_mode',
        message: "pin 4 on board brd_1 doesn't support servo; servo pins are 2-19",
      }),
    )

    // Open the Add-device dialog and emit its 'submit' as the real form would.
    const addDeviceBtn = wrapper.findAll('button').find((b) => b.text() === 'Add device')
    expect(addDeviceBtn).toBeTruthy()
    await addDeviceBtn!.trigger('click')
    const dialog = wrapper.findComponent(DeviceFormDialog)
    expect(dialog.exists()).toBe(true)
    dialog.vm.$emit('submit', { name: 'Pan', boardId: 'brd_1', pin: 4, role: 'servo', options: {} })
    await flushPromises()

    expect(createSpy).toHaveBeenCalled()
    const { toasts } = useToasts()
    expect(toasts.length).toBeGreaterThan(0)
    const toast = toasts[toasts.length - 1]!
    expect(toast.severity).toBe('warning')
    expect(toast.message).toContain('servo')
    expect(toast.message).toContain('pin 4')
  })
})

function pipeline(overrides: Partial<PipelineSummary> = {}): PipelineSummary {
  return {
    id: 'pl_1',
    name: 'Sprinkler loop',
    enabled: true,
    tickMs: 20,
    nodeCount: 4,
    status: 'running',
    ...overrides,
  }
}

describe('HomeView: pipelines table paused status', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the amber badge, exposes the detail, and keeps the enabled toggle on', async () => {
    const { wrapper, pipelines } = await mountHome()
    pipelines.pipelines = [
      pipeline({ status: 'paused', detail: 'device dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)' }),
    ]
    await wrapper.vm.$nextTick()

    const badge = wrapper.findComponent(StatusBadge)
    expect(badge.exists()).toBe(true)
    expect(badge.props('status')).toBe('paused')
    expect(badge.find('.dot.warn').exists()).toBe(true)

    expect(wrapper.text()).toContain('device dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)')

    const toggle = wrapper.find('.toggle-switch')
    expect(toggle.attributes('aria-checked')).toBe('true')
  })

  it('does not surface any detail line for a plain running pipeline', async () => {
    const { wrapper, pipelines } = await mountHome()
    pipelines.pipelines = [pipeline({ status: 'running' })]
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.pipeline-detail').exists()).toBe(false)
  })
})
