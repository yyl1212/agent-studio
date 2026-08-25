import { describe, expect, it } from 'vitest'

import { readRunSearch, readWorkflowSearch, writeRunSearch, writeWorkflowSearch } from './managementQuery'

describe('management query model', () => {
	it('接受 cancelling 和完整五状态筛选', () => {
		const parsed = readRunSearch(new URLSearchParams('status=cancelled&status=cancelling&status=completed&status=failed&status=running'))
		expect(parsed.hadInvalid).toBe(false)
		expect(parsed.query.statuses).toEqual(['cancelled', 'cancelling', 'completed', 'failed', 'running'])
	})

	it('移除非法工作流参数并回退默认值', () => {
		const parsed = readWorkflowSearch(new URLSearchParams([
			['q', 'a'], ['q', 'b'], ['state', 'deleted'], ['limit', '101'],
			['cursor', 'x'.repeat(513)], ['unknown', 'true'],
		]))
		expect(parsed.query).toEqual({ q: '', state: 'active', limit: 50 })
		expect(parsed.params.toString()).toBe('state=active&limit=50')
		expect(parsed.hadInvalid).toBe(true)
	})

	it('工作流参数规范往返且筛选变化时可清空游标', () => {
		const canonical = new URLSearchParams('q=Agent&state=all&cursor=next&limit=25')
		const parsed = readWorkflowSearch(canonical)
		expect(parsed.hadInvalid).toBe(false)
		expect(parsed.params.toString()).toBe(canonical.toString())
		expect(writeWorkflowSearch({ ...parsed.query, state: 'archived', cursor: undefined }).toString()).toBe('q=Agent&state=archived&limit=25')
	})

	it('移除非法运行参数并回退默认值', () => {
		const parsed = readRunSearch(new URLSearchParams([
			['workflowId', 'bad'], ['runId', 'bad'], ['status', 'unknown'], ['mode', 'batch'],
			['startedAfter', '2026-02-30T00:00:00Z'], ['startedBefore', 'not-a-date'],
			['limit', '0'], ['cursor', 'x'.repeat(513)], ['unknown', 'true'],
		]))
		expect(parsed.query).toEqual({ statuses: [], modes: [], limit: 50 })
		expect(parsed.params.toString()).toBe('limit=50')
		expect(parsed.hadInvalid).toBe(true)
	})

	it('运行参数规范往返、排序去重并约束时间跨度', () => {
		const canonical = new URLSearchParams('workflowId=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa&status=failed&status=running&mode=debug&mode=test&startedAfter=2026-08-01T00%3A00%3A00.000Z&startedBefore=2026-08-25T00%3A00%3A00.000Z&runId=11111111-1111-4111-8111-111111111111&cursor=next&limit=25')
		const parsed = readRunSearch(canonical)
		expect(parsed.hadInvalid).toBe(false)
		expect(parsed.params.toString()).toBe(canonical.toString())
		expect(writeRunSearch({ ...parsed.query, statuses: ['running', 'failed', 'failed'], cursor: undefined }).toString()).toContain('status=failed&status=running')
		expect(writeRunSearch({ ...parsed.query, statuses: ['running'], cursor: undefined }).toString()).not.toContain('cursor=')

		const tooWide = readRunSearch(new URLSearchParams('startedAfter=2026-01-01T00%3A00%3A00.000Z&startedBefore=2026-04-02T00%3A00%3A00.000Z'))
		expect(tooWide.query.startedAfter).toBeUndefined()
		expect(tooWide.query.startedBefore).toBeUndefined()
		expect(tooWide.hadInvalid).toBe(true)
	})

	it('把大小写 UUID、偏移时间、重复枚举标记为已规范化', () => {
		const parsed = readRunSearch(new URLSearchParams('workflowId=AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA&status=failed&status=failed&startedAfter=2026-08-01T08%3A00%3A00%2B08%3A00'))
		expect(parsed.query.workflowId).toBe('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')
		expect(parsed.query.statuses).toEqual(['failed'])
		expect(parsed.query.startedAfter).toBe('2026-08-01T00:00:00.000Z')
		expect(parsed.hadInvalid).toBe(true)
	})
})
