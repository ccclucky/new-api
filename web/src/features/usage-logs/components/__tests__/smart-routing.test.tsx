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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'
import { ModelBadge, SmartRoutingDetails } from '../model-badge'

const queryClients: QueryClient[] = []

function makeLog(other: LogOtherData): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'claude-sonnet-4.5',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 0,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDialog(other: LogOtherData, isAdmin: boolean): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClients.push(queryClient)

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog(other)}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) {
    queryClient.clear()
  }
  queryClients.length = 0
})

describe('smart routing log display', () => {
  test('badge shows the Auto chip for routed requests', () => {
    render(
      <ModelBadge
        modelName='claude-sonnet-4.5'
        smartRouting={{ from: 'auto', to: 'claude-sonnet-4.5', by: 'jev' }}
      />
    )
    expect(screen.getByText('Auto')).toBeInTheDocument()
  })

  test('badge omits the Auto chip for direct model calls', () => {
    render(<ModelBadge modelName='claude-sonnet-4.5' />)
    expect(screen.queryByText('Auto')).toBeNull()
  })

  test('details name the routed model and the chooser', () => {
    render(
      <SmartRoutingDetails
        summary={{ from: 'auto', to: 'claude-sonnet-4.5', by: 'jev' }}
      />
    )
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByText('claude-sonnet-4.5')).toBeInTheDocument()
    expect(screen.getByText('Decision model')).toBeInTheDocument()
  })

  test('details show the chooser for a fallback routing', () => {
    render(
      <SmartRoutingDetails
        summary={{ from: 'auto', to: 'gpt-5.1', by: 'fallback' }}
      />
    )
    expect(screen.getByText('Fallback')).toBeInTheDocument()
  })

  test('consume dialog renders the summary for the log owner', () => {
    renderDialog(
      { smart_routing: { from: 'auto', to: 'claude-sonnet-4.5', by: 'jev' } },
      false
    )
    expect(screen.getByText('Smart routing')).toBeInTheDocument()
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByText('claude-sonnet-4.5')).toBeInTheDocument()
    // The full decision is admin-only.
    expect(screen.queryByText('Candidate models')).toBeNull()
  })

  test('admin dialog additionally renders the routing decision', () => {
    renderDialog(
      {
        smart_routing: { from: 'auto', to: 'claude-sonnet-4.5', by: 'jev' },
        admin_info: {
          smart_routing: {
            pool_size: 8,
            chosen_by: 'jev',
            model: 'claude-sonnet-4.5',
            latency_ms: 320,
            jev_model: 'jev-1.13.0',
          },
        },
      },
      true
    )
    expect(screen.getByText('Routing decision')).toBeInTheDocument()
    expect(screen.getByText('8')).toBeInTheDocument()
    expect(screen.getByText('320 ms')).toBeInTheDocument()
    expect(screen.getByText('jev-1.13.0')).toBeInTheDocument()
  })
})
