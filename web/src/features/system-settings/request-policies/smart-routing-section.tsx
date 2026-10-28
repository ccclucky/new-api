/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useMemo } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import {
  SettingsForm,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const OPTION_KEYS = {
  enabled: 'auto_routing_setting.enabled',
  virtualModel: 'auto_routing_setting.virtual_model',
  baseUrl: 'auto_routing_setting.base_url',
  apiKey: 'auto_routing_setting.api_key',
  fallbackModel: 'auto_routing_setting.fallback_model',
  timeoutMs: 'auto_routing_setting.timeout_ms',
} as const

function createSmartRoutingSchema(t: (key: string) => string) {
  return z.object({
    enabled: z.boolean(),
    virtualModel: z.string().min(1, t('Model name cannot be empty')),
    baseUrl: z.url(t('Invalid URL')),
    apiKey: z.string(),
    fallbackModel: z.string(),
    timeoutMs: z.coerce.number().int().min(100).max(60000),
  })
}

type Values = z.infer<ReturnType<typeof createSmartRoutingSchema>>

export function SmartRoutingSection() {
  const { t } = useTranslation()
  const optionsQuery = useSystemOptions()
  if (optionsQuery.isPending) return <LoadingState />
  if (optionsQuery.isError) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void optionsQuery.refetch()}
      />
    )
  }
  return <SmartRoutingForm options={optionsQuery.data.data} />
}

function SmartRoutingForm({
  options,
}: {
  options: Array<{ key: string; value: string }>
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const schema = useMemo(() => createSmartRoutingSchema(t), [t])
  const values = new Map(options.map((option) => [option.key, option.value]))
  // The stored API key is never echoed back; leave the field blank to keep it.
  const hasStoredApiKey = (values.get(OPTION_KEYS.apiKey) || '') !== ''
  const defaults: Values = {
    enabled: values.get(OPTION_KEYS.enabled) === 'true',
    virtualModel: values.get(OPTION_KEYS.virtualModel) || 'auto',
    baseUrl: values.get(OPTION_KEYS.baseUrl) || 'https://api.typesafe.ai',
    apiKey: '',
    fallbackModel: values.get(OPTION_KEYS.fallbackModel) || '',
    timeoutMs: Number(values.get(OPTION_KEYS.timeoutMs)) || 500,
  }

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: defaults,
  })
  const enabled = form.watch('enabled')

  async function onSubmit(submitted: Values) {
    const names = Object.keys(OPTION_KEYS) as Array<keyof typeof OPTION_KEYS>
    const changed = names.filter(
      (name) => String(defaults[name]) !== String(submitted[name])
    )
    if (changed.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    try {
      for (const name of changed) {
        await updateOption.mutateAsync({
          key: OPTION_KEYS[name],
          value: String(submitted[name]),
        })
      }
      form.reset({ ...submitted, apiKey: '' })
    } catch {
      // useUpdateOption already surfaces the failure toast.
    }
  }

  return (
    <SettingsSection title={t('Smart routing')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            onReset={() => form.reset(defaults)}
            isResetDisabled={!form.formState.isDirty}
          />
          <div className='space-y-4'>
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchField
                  controlId='smart-routing-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  label={t('Enable smart routing')}
                  description={t(
                    'Clients may call the virtual model name to delegate model choice. The last user message is sent to the third-party decision model; enable only with consent.'
                  )}
                />
              )}
            />
            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='virtualModel'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Virtual model name')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='auto' />
                    </FormControl>
                    <FormDescription>
                      {t('Clients send this name to delegate model choice.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='fallbackModel'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Fallback model')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('e.g. gpt-5.1')}
                        disabled={!enabled}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Used when the decision model fails or answers outside the candidate pool.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='baseUrl'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Decision model base URL')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder='https://api.typesafe.ai'
                        disabled={!enabled}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='apiKey'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Decision model API key')}</FormLabel>
                    <FormControl>
                      <Input
                        type='password'
                        {...field}
                        placeholder={
                          hasStoredApiKey
                            ? t('(leave blank to keep current key)')
                            : ''
                        }
                        disabled={!enabled}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='timeoutMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Decision timeout (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={100}
                        max={60000}
                        step={100}
                        disabled={!enabled}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('On timeout the fallback model is used.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
