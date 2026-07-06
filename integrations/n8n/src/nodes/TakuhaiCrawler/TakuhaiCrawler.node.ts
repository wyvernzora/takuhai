import type {
	IDataObject,
	IExecuteFunctions,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';
import { LoggerProxy as Logger } from 'n8n-workflow';

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
		name: 'takuhaiCrawlerV2',
		icon: 'file:takuhai.svg',
		group: ['transform'],
		version: 1,
		subtitle: '={{$parameter["operation"] + " (" + $parameter["resource"] + ")"}}',
		description: 'Fetch a page of posts from a takuhai-shaped crawler (POST /crawl)',
		codex: {
			categories: ['Core Nodes'],
			subcategories: {
				'Core Nodes': ['Helpers'],
			},
		},
		defaults: { name: 'Takuhai Crawler' },
		inputs: ['main'],
		outputs: ['main'],
		credentials: [{ name: 'takuhaiCrawlerApi', required: true }],
		properties: [
			{
				displayName: 'Resource',
				name: 'resource',
				type: 'options',
				noDataExpression: true,
				options: [{ name: 'Crawler', value: 'crawler' }],
				default: 'crawler',
			},
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				options: [{ name: 'Crawl', value: 'crawl', action: 'Crawl posts' }],
				default: 'crawl',
			},
			{
				displayName: 'Page Size',
				name: 'pageSize',
				type: 'number',
				typeOptions: { minValue: 1, maxValue: 200 },
				default: 50,
				description: 'Posts to fetch per call (1–200); the crawler clamps out-of-range values',
			},
			{
				displayName: 'Cursor',
				name: 'cursor',
				type: 'string',
				default: '',
				description: 'Opaque cursor from a prior call (n8n owns crawl state); blank to start',
			},
			{
				displayName: 'Lookback',
				name: 'lookback',
				type: 'string',
				default: '',
				placeholder: '12h',
				description:
					'Drop posts older than now − lookback (extended Go duration, e.g. 12h, 30d, 2w); blank = no time limit',
			},
		],
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const items = this.getInputData();
		const credentials = await this.getCredentials('takuhaiCrawlerApi');
		const baseUrl = String(credentials.baseUrl).replace(/\/+$/, '');
		const out: INodeExecutionData[] = [];

		Logger.info('Takuhai crawler node execution started', { item_count: items.length });
		for (let i = 0; i < items.length; i++) {
			try {
				const body = {
					page_size: this.getNodeParameter('pageSize', i),
					cursor: this.getNodeParameter('cursor', i),
					lookback: this.getNodeParameter('lookback', i),
				};
				Logger.info('Takuhai crawler request started', {
					item_index: i,
					cursor: body.cursor,
					page_size: body.page_size,
					lookback: body.lookback,
				});
				const res = (await this.helpers.httpRequest({
					method: 'POST',
					url: `${baseUrl}/crawl`,
					body,
					json: true,
				})) as IDataObject;
				const posts = Array.isArray(res.posts) ? res.posts.length : 0;
				Logger.info('Takuhai crawler page fetched', {
					item_index: i,
					post_count: posts,
					has_more: res.has_more === true,
					has_next_cursor: typeof res.next_cursor === 'string' && res.next_cursor !== '',
					cursor: body.cursor,
					next_cursor: typeof res.next_cursor === 'string' ? res.next_cursor : '',
				});
				out.push({ json: res, pairedItem: { item: i } });
			} catch (error) {
				const meta = {
					item_index: i,
					err: (error as Error).message,
				};
				if (this.continueOnFail()) {
					Logger.warn('Takuhai crawler node item failed', meta);
					out.push({ json: { error: (error as Error).message }, pairedItem: { item: i } });
					continue;
				}
				Logger.debug('Takuhai crawler node item failed', meta);
				throw error;
			}
		}
		return [out];
	}
}
