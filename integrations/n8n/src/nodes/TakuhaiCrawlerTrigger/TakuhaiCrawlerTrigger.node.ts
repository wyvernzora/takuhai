import type {
	IDataObject,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
	IPollFunctions,
} from 'n8n-workflow';
import { LoggerProxy as Logger } from 'n8n-workflow';

const CRED = 'takuhaiCrawlerApi';
const MAX_PAGES_PER_POLL = 25;

type CrawlState = IDataObject & {
	cursor?: string;
	floor?: string;
	lastSeenPublishedAt?: string;
	passStartedAt?: string;
	pendingLastSeen?: string;
	resetStateApplied?: boolean;
};

/**
 * Takuhai Crawler - n8n owns the crawl watermark and emits a batch only
 * when a crawler has posts newer than that watermark.
 */
export class TakuhaiCrawlerTrigger implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'Takuhai Crawler Trigger',
		name: 'takuhaiCrawlerV2Trigger',
		icon: 'file:takuhai.svg',
		group: ['trigger'],
		version: 1,
		subtitle: '={{"crawl: " + $parameter["batchSize"] + " posts"}}',
		description: 'Polls a takuhai-shaped crawler and emits new posts in batches',
		codex: {
			categories: ['Core Nodes'],
			subcategories: {
				'Core Nodes': ['Helpers'],
			},
		},
		defaults: { name: 'Takuhai Crawler Trigger' },
		polling: true,
		inputs: [],
		outputs: ['main'],
		credentials: [{ name: CRED, required: true }],
		properties: [
			{
				displayName: 'Batch Size',
				name: 'batchSize',
				type: 'number',
				typeOptions: { minValue: 1, maxValue: 1000 },
				default: 50,
				description: 'Max new posts to emit per triggered execution',
			},
			{
				displayName: 'Page Size',
				name: 'pageSize',
				type: 'number',
				typeOptions: { minValue: 1, maxValue: 200 },
				default: 50,
				description: 'Max posts to ask the crawler for per HTTP call',
			},
			{
				displayName: 'Lookback',
				name: 'lookback',
				type: 'string',
				default: '24h',
				placeholder: '24h',
				description:
					'First-poll/restart floor as an extended duration such as 12h, 30d, or 2w',
			},
			{
				displayName: 'Options',
				name: 'options',
				type: 'collection',
				placeholder: 'Add option',
				default: {},
				options: [
					{
						displayName: 'Reset State Before Poll',
						name: 'resetState',
						type: 'boolean',
						default: false,
						description:
							"Clear this trigger node's saved cursor and watermark once while enabled. Toggle off and on to reset again.",
					},
				],
			},
		],
	};

	async poll(this: IPollFunctions): Promise<INodeExecutionData[][] | null> {
		const state = this.getWorkflowStaticData('node') as CrawlState;
		const options = this.getNodeParameter('options', {}) as IDataObject;
		if (options.resetState === true && state.resetStateApplied !== true) {
			clearState(state);
			state.resetStateApplied = true;
			Logger.info('Takuhai crawler trigger state reset');
		} else if (options.resetState !== true) {
			state.resetStateApplied = false;
		}

		const batchSize = clampNumber(this.getNodeParameter('batchSize', 50), 1, 1000);
		const pageSize = clampNumber(this.getNodeParameter('pageSize', 50), 1, 200);
		const lookback = String(this.getNodeParameter('lookback', '24h'));
		const lookbackMs = parseDurationMs(lookback);
		startPassIfNeeded(state, new Date(), lookbackMs);

		const credentials = await this.getCredentials(CRED);
		const baseUrl = String(credentials.baseUrl).replace(/\/+$/, '');
		const posts: IDataObject[] = [];
		let finished = false;
		let pagesFetched = 0;

		for (let pages = 0; pages < MAX_PAGES_PER_POLL && posts.length < batchSize; pages++) {
			const remaining = batchSize - posts.length;
			const cursor = state.cursor ?? '';
			Logger.info('Takuhai crawler trigger request started', {
				page_index: pages,
				cursor,
				page_size: Math.min(pageSize, remaining),
			});
			const res = (await this.helpers.httpRequest({
				method: 'POST',
				url: `${baseUrl}/crawl`,
				body: {
					page_size: Math.min(pageSize, remaining),
					cursor,
				},
				json: true,
			})) as IDataObject;
			pagesFetched++;

			const pagePosts = Array.isArray(res.posts) ? (res.posts as IDataObject[]) : [];
			const nextCursor = typeof res.next_cursor === 'string' ? res.next_cursor : '';
			Logger.info('Takuhai crawler trigger response received', {
				page_index: pages,
				cursor,
				next_cursor: nextCursor,
				post_count: pagePosts.length,
				has_more: res.has_more === true,
			});
			for (const post of pagePosts) {
				const publishedAt = publishedAtMs(post);
				if (publishedAt !== undefined && publishedAt <= Date.parse(state.floor ?? '')) {
					finished = true;
					break;
				}
				if (publishedAt === undefined || publishedAt <= Date.parse(state.passStartedAt ?? '')) {
					posts.push(post);
				}
			}

			if (finished) break;
			if (res.has_more !== true || nextCursor === '') {
				finished = true;
				break;
			}
			state.cursor = nextCursor;
		}

		if (finished) {
			finishPass(state);
		}

		if (posts.length === 0) {
			Logger.debug('Takuhai crawler trigger poll completed with no posts', {
				pages_fetched: pagesFetched,
				finished,
				cursor: state.cursor ?? '',
			});
			return null;
		}
		Logger.info('Takuhai crawler trigger emitted posts', {
			post_count: posts.length,
			pages_fetched: pagesFetched,
			finished,
			cursor: state.cursor ?? '',
		});
		return [[{ json: { posts, count: posts.length } }]];
	}
}

function startPassIfNeeded(state: CrawlState, now: Date, lookbackMs: number): void {
	if (state.passStartedAt) return;
	const passStartedAt = now.toISOString();
	state.passStartedAt = passStartedAt;
	state.pendingLastSeen = passStartedAt;
	state.floor =
		nonEmptyString(state.lastSeenPublishedAt)
			? state.lastSeenPublishedAt
			: new Date(now.getTime() - lookbackMs).toISOString();
	state.cursor = '';
}

function finishPass(state: CrawlState): void {
	state.lastSeenPublishedAt = state.pendingLastSeen;
	state.cursor = '';
	state.floor = '';
	state.passStartedAt = '';
	state.pendingLastSeen = '';
}

function clearState(state: CrawlState): void {
	state.cursor = '';
	state.floor = '';
	state.lastSeenPublishedAt = '';
	state.passStartedAt = '';
	state.pendingLastSeen = '';
}

function nonEmptyString(value: unknown): value is string {
	return typeof value === 'string' && value !== '';
}

function publishedAtMs(post: IDataObject): number | undefined {
	const value = post.published_at;
	if (typeof value !== 'string') return undefined;
	const ms = Date.parse(value);
	return Number.isNaN(ms) ? undefined : ms;
}

function clampNumber(value: unknown, min: number, max: number): number {
	const n = Number(value);
	if (!Number.isFinite(n)) return min;
	return Math.min(max, Math.max(min, Math.trunc(n)));
}

function parseDurationMs(input: string): number {
	const s = input.trim();
	const pattern = /(\d+(?:\.\d+)?)(ms|s|m|h|d|w)/g;
	let total = 0;
	let consumed = '';
	for (const match of s.matchAll(pattern)) {
		consumed += match[0];
		const value = Number.parseFloat(match[1]);
		const unit = match[2];
		const factor =
			unit === 'ms'
				? 1
				: unit === 's'
					? 1000
					: unit === 'm'
						? 60_000
						: unit === 'h'
							? 3_600_000
							: unit === 'd'
								? 86_400_000
								: 604_800_000;
		total += value * factor;
	}
	if (s === '' || consumed !== s || total <= 0) {
		throw new Error('Lookback must be a duration like 24h, 30d, or 1h30m');
	}
	return total;
}
