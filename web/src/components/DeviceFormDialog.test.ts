import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DeviceFormDialog from './DeviceFormDialog.vue'

describe('DeviceFormDialog: numeric re-seed on role switch', () => {
  it('re-seeds initial/safeValue to a number when switching digital-out -> servo', async () => {
    const wrapper = mount(DeviceFormDialog)

    const roleSelect = wrapper.find('#dev-role')
    await roleSelect.setValue('digital-out')
    // Boolean role: initial is a checkbox, no numeric input yet.
    expect(wrapper.find('#dev-initial[type="number"]').exists()).toBe(false)
    expect(wrapper.find('label.field-inline input[type="checkbox"]').exists()).toBe(true)

    await roleSelect.setValue('servo')

    // Numeric role: initial re-seeds to a number input defaulting to 0.
    const initialInput = wrapper.find('#dev-initial')
    expect(initialInput.attributes('type')).toBe('number')
    expect((initialInput.element as HTMLInputElement).value).toBe('0')
    expect(initialInput.attributes('max')).toBe('180')

    // Servo-only pulse-width fields appear with their documented defaults.
    const minPulse = wrapper.find('#dev-min-pulse')
    const maxPulse = wrapper.find('#dev-max-pulse')
    expect((minPulse.element as HTMLInputElement).value).toBe('544')
    expect((maxPulse.element as HTMLInputElement).value).toBe('2400')
  })

  it('re-seeds initial back to boolean when switching servo -> digital-out', async () => {
    const wrapper = mount(DeviceFormDialog)
    const roleSelect = wrapper.find('#dev-role')

    await roleSelect.setValue('servo')
    expect(wrapper.find('#dev-initial[type="number"]').exists()).toBe(true)

    await roleSelect.setValue('digital-out')
    expect(wrapper.find('label.field-inline input[type="checkbox"]').exists()).toBe(true)
    expect(wrapper.find('#dev-initial[type="number"]').exists()).toBe(false)
  })

  it('keeps pwm-out\'s own 0-255 range distinct from servo\'s 0-180', async () => {
    const wrapper = mount(DeviceFormDialog)
    const roleSelect = wrapper.find('#dev-role')

    await roleSelect.setValue('pwm-out')
    const pwmInitial = wrapper.find('#dev-initial')
    expect(pwmInitial.attributes('max')).toBe('255')
    // No servo-only pulse-width fields for pwm-out.
    expect(wrapper.find('#dev-min-pulse').exists()).toBe(false)
  })

  it('emits minPulseUs/maxPulseUs in the submitted options for servo', async () => {
    const wrapper = mount(DeviceFormDialog)
    await wrapper.find('#dev-name').setValue('Pan servo')
    await wrapper.find('#dev-role').setValue('servo')
    await wrapper.find('#dev-pin').setValue(9)

    await wrapper.find('form').trigger('submit.prevent')

    const emitted = wrapper.emitted('submit')
    expect(emitted).toBeTruthy()
    const payload = emitted![0]![0] as { options: { minPulseUs?: number; maxPulseUs?: number; initial?: unknown } }
    expect(payload.options.minPulseUs).toBe(544)
    expect(payload.options.maxPulseUs).toBe(2400)
    expect(payload.options.initial).toBe(0)
  })
})
