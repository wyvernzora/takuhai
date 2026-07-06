import type {
	IDataObject,
	IExecuteFunctions,
	IHttpRequestMethods,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';
import { LoggerProxy as Logger } from 'n8n-workflow';

const CRED = 'takuhaiApi';

export class Takuhai implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'Takuhai',
		name: 'takuhai',
		icon: 'file:takuhai.svg',
		group: ['transform'],
		version: 1,
		subtitle: '={{$parameter["operation"] + " (" + $parameter["resource"] + ")"}}',
		description: 'Ingest posts and drive the takuhai match loop',
		codex: {
			categories: ['Core Nodes'],
			subcategories: {
				'Core Nodes': ['Helpers'],
			},
		},
		defaults: { name: 'Takuhai' },
		inputs: ['main'],
		outputs: ['main'],
		credentials: [{ name: CRED, required: true }],
		properties: [
			{
				displayName: 'Resource',
				name: 'resource',
				type: 'options',
				noDataExpression: true,
				options: [
					{ name: 'Releases', value: 'releases' },
					{ name: 'Queue', value: 'queue' },
				],
				default: 'releases',
			},
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				displayOptions: { show: { resource: ['releases'] } },
				options: [
					{ name: 'Ingest', value: 'ingest', action: 'Ingest releases' },
					{ name: 'Get Release', value: 'get', action: 'Get a release' },
					{
						name: 'Get Magnet Link',
						value: 'getMagnetLink',
						action: 'Get a release magnet link',
					},
				],
				default: 'ingest',
			},
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				displayOptions: { show: { resource: ['queue'] } },
				options: [
					{ name: 'Claim', value: 'claim', action: 'Claim a batch of releases' },
					{ name: 'Submit Dispositions', value: 'submit', action: 'Submit disposition results' },
					{ name: 'Get Queue Stats', value: 'queueStats', action: 'Read the queue counts' },
				],
				default: 'claim',
			},
			{
				displayName: 'Posts',
				name: 'posts',
				type: 'json',
				default: '={{ $json.posts }}',
				required: true,
				description: 'The posts payload from a Crawler page, forwarded as-is to /ingest',
				displayOptions: { show: { resource: ['releases'], operation: ['ingest'] } },
			},
			{
				displayName: 'Limit',
				name: 'limit',
				type: 'number',
				typeOptions: { minValue: 1 },
				default: 10,
				description: 'Max releases to claim',
				displayOptions: { show: { resource: ['queue'], operation: ['claim'] } },
			},
			{
				displayName: 'Lease Seconds',
				name: 'lease_seconds',
				type: 'number',
				default: 300,
				description: 'Lease length; honored if supplied, else a server default',
				displayOptions: { show: { resource: ['queue'], operation: ['claim'] } },
			},
			{
				displayName: 'Body',
				name: 'body',
				type: 'json',
				default: '={{ $json }}',
				required: true,
				description:
					'A single /submit body, an array of /submit bodies, an object with items, or a structured-output object with output.items',
				displayOptions: { show: { resource: ['queue'], operation: ['submit'] } },
			},
			{
				displayName: 'Infohash',
				name: 'infohash',
				type: 'string',
				default: '={{ $json.infohash }}',
				required: true,
				displayOptions: { show: { resource: ['releases'], operation: ['get', 'getMagnetLink'] } },
			},
		],
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const items = this.getInputData();
		const operation = this.getNodeParameter('operation', 0) as string;

		const credentials = await this.getCredentials(CRED);
		const baseUrl = String(credentials.baseUrl).replace(/\/+$/, '');
		Logger.info('Takuhai node execution started', { operation, item_count: items.length });

		const call = (method: IHttpRequestMethods, path: string, body?: IDataObject) =>
			this.helpers.httpRequest({
				method,
				url: `${baseUrl}${path}`,
				body,
				json: true,
			}) as Promise<IDataObject>;

		if (operation === 'queueStats') {
			const stats = await call('GET', '/queue/stats');
			Logger.info('Takuhai queue stats fetched', {
				available: stats.available,
				leased: stats.leased,
				exhausted: stats.exhausted,
			});
			return [[{ json: stats }]];
		}

		if (operation === 'claim') {
			const res = await call('POST', '/queue/claim', {
				limit: this.getNodeParameter('limit', 0),
				lease_seconds: this.getNodeParameter('lease_seconds', 0),
			});
			const claimed = (res.items as IDataObject[]) ?? [];
			Logger.info('Takuhai queue claim completed', { claimed_count: claimed.length });
			return [[{ json: { items: claimed, count: claimed.length } }]];
		}

		const out: INodeExecutionData[] = [];
		for (let i = 0; i < items.length; i++) {
			try {
				if (operation === 'ingest') {
					const posts = this.getNodeParameter('posts', i);
					const res = await call('POST', '/ingest', { posts });
					Logger.info('Takuhai ingest completed', {
						item_index: i,
						post_count: Array.isArray(posts) ? posts.length : undefined,
						new_count: (res.batch as IDataObject | undefined)?.new,
						updated_count: (res.batch as IDataObject | undefined)?.updated,
						duplicate_count: (res.batch as IDataObject | undefined)?.duplicate,
					});
					out.push({ json: res, pairedItem: { item: i } });
					continue;
				}

				if (operation === 'getMagnetLink') {
					const infohash = String(this.getNodeParameter('infohash', i));
					const res = await call('GET', `/magnets/${encodeURIComponent(infohash)}`);
					Logger.info('Takuhai magnet lookup completed', { item_index: i, infohash });
					out.push({ json: res, pairedItem: { item: i } });
					continue;
				}

				if (operation === 'get') {
					const infohash = String(this.getNodeParameter('infohash', i));
					const res = await call('GET', `/releases/${encodeURIComponent(infohash)}`);
					Logger.info('Takuhai release lookup completed', { item_index: i, infohash });
					out.push({ json: res, pairedItem: { item: i } });
					continue;
				}

				const payload = submitPayload(this.getNodeParameter('body', i));
				const submitted: IDataObject[] = [];
				for (const body of payload.bodies) {
					submitted.push(await submitOne(call, body));
				}
				Logger.info('Takuhai submissions completed', {
					item_index: i,
					submit_count: submitted.length,
					conflict_count: submitted.filter((item) => item.ok === false).length,
				});
				out.push({
					json: { items: submitted, count: submitted.length },
					pairedItem: { item: i },
				});
			} catch (error) {
				const meta = {
					operation,
					item_index: i,
					status_code: statusCode(error),
					err: (error as Error).message,
				};
				if (this.continueOnFail()) {
					Logger.warn('Takuhai node item failed', meta);
					out.push({ json: { error: (error as Error).message }, pairedItem: { item: i } });
					continue;
				}
				Logger.debug('Takuhai node item failed', meta);
				throw error;
			}
		}
		return [out];
	}
}

type HTTPCall = (method: IHttpRequestMethods, path: string, body?: IDataObject) => Promise<IDataObject>;

async function submitOne(call: HTTPCall, body: IDataObject): Promise<IDataObject> {
	try {
		await call('POST', '/submit', body);
		return { infohash: submitInfohash(body), metadataRef: submitMetadataRef(body), ok: true };
	} catch (error) {
		if (statusCode(error) === 409) {
			return { infohash: submitInfohash(body), metadataRef: submitMetadataRef(body), ok: false, error: 'conflict' };
		}
		throw error;
	}
}

function submitPayload(input: unknown): { bodies: IDataObject[] } {
	if (typeof input === 'string') {
		return submitPayload(JSON.parse(input));
	}
	if (Array.isArray(input)) {
		return { bodies: input.map(asSubmitObject) };
	}
	const body = asSubmitObject(input);
	const items = body.items ?? outputItems(body);
	if (Array.isArray(items)) {
		return { bodies: items.map(asSubmitObject) };
	}
	return { bodies: [body] };
}

function asSubmitObject(input: unknown): IDataObject {
	if (input && typeof input === 'object' && !Array.isArray(input)) {
		return input as IDataObject;
	}
	throw new Error('Submit body must be an object, an array of objects, or an object with items');
}

function outputItems(body: IDataObject): unknown {
	const output = body.output;
	if (output && typeof output === 'object' && !Array.isArray(output)) {
		return (output as IDataObject).items;
	}
	return undefined;
}

function submitInfohash(body: IDataObject): string {
	const infohash = body.infohash;
	return typeof infohash === 'string' ? infohash : '';
}

function submitMetadataRef(body: IDataObject): string {
	const ref = body.ref;
	return typeof ref === 'string' ? ref : '';
}

function statusCode(error: unknown): number | undefined {
	if (!error || typeof error !== 'object') return undefined;
	const err = error as IDataObject;
	const response = err.response;
	const nested = response && typeof response === 'object' && !Array.isArray(response) ? (response as IDataObject) : {};
	for (const value of [err.statusCode, err.httpCode, nested.statusCode, nested.status]) {
		if (typeof value === 'number') return value;
		if (typeof value === 'string') {
			const parsed = Number.parseInt(value, 10);
			if (!Number.isNaN(parsed)) return parsed;
		}
	}
	return undefined;
}
