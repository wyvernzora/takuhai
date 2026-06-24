import type { INodeType, INodeTypeDescription } from 'n8n-workflow';

/**
 * Takuhai Crawler — drives ANY takuhai-shaped crawler over the shared POST /crawl
 * contract; source-agnostic by design (DMHY first, nyaa next). Declarative: one call,
 * and the whole page `{posts, next_cursor, has_more}` is the output item — posts are
 * opaque (n8n shuttles them to /ingest), and keeping the page whole means the cursor +
 * has_more flag survive even an empty terminal page so a backfill loop can stop on
 * `has_more=false` (lookback boundary OR archive floor). The crawler is stateless: n8n
 * owns the cursor (pass it in, store next_cursor out) and the page size + lookback depth.
 */
export class TakuhaiCrawler implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'Takuhai Crawler',
		name: 'takuhaiCrawler',
		icon: 'file:takuhai.svg',
		group: ['transform'],
		version: 1,
		subtitle: '={{"crawl: " + $parameter["pageSize"] + " posts"}}',
		description: 'Fetch a page of posts from a takuhai-shaped crawler (POST /crawl)',
		defaults: { name: 'Takuhai Crawler' },
		inputs: ['main'],
		outputs: ['main'],
		credentials: [{ name: 'takuhaiCrawlerApi', required: true }],
		requestDefaults: {
			baseURL: '={{$credentials.baseUrl}}',
			method: 'POST',
			url: '/crawl',
			headers: { 'Content-Type': 'application/json' },
		},
		properties: [
			{
				displayName: 'Page Size',
				name: 'pageSize',
				type: 'number',
				typeOptions: { minValue: 1, maxValue: 200 },
				default: 50,
				description: 'Posts to fetch per call (1–200); the crawler clamps out-of-range values',
				routing: { send: { type: 'body', property: 'page_size' } },
			},
			{
				displayName: 'Cursor',
				name: 'cursor',
				type: 'string',
				default: '',
				description: 'Opaque cursor from a prior call (n8n owns crawl state); blank to start',
				routing: { send: { type: 'body', property: 'cursor' } },
			},
			{
				displayName: 'Lookback',
				name: 'lookback',
				type: 'string',
				default: '',
				placeholder: '12h',
				description:
					'Drop posts older than now − lookback (extended Go duration, e.g. 12h, 30d, 2w); blank = no time limit',
				routing: { send: { type: 'body', property: 'lookback' } },
			},
		],
	};
}
