import type {
	IDataObject,
	IExecuteFunctions,
	IHttpRequestMethods,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';

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
					{ name: 'Ingest', value: 'ingest' },
					{ name: 'Queue', value: 'queue' },
				],
				default: 'queue',
			},
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				displayOptions: { show: { resource: ['ingest'] } },
				options: [
					{
						name: 'Ingest Posts',
						value: 'ingestPosts',
						action: 'Ingest a batch of raw posts',
						description: 'Forward a page of posts; takuhai dedups and queues them',
					},
				],
				default: 'ingestPosts',
			},
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				displayOptions: { show: { resource: ['queue'] } },
				options: [
					{ name: 'Claim', value: 'claim', action: 'Claim a batch of releases' },
					{ name: 'Submit', value: 'submit', action: 'Submit a matcher result' },
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
				displayOptions: { show: { resource: ['ingest'], operation: ['ingestPosts'] } },
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
				displayName: 'Infohash',
				name: 'infohash',
				type: 'string',
				default: '={{ $json.infohash }}',
				required: true,
				description: 'The release infohash (40-hex v1 btih)',
				displayOptions: { show: { resource: ['queue'], operation: ['submit'] } },
			},
			{
				displayName: 'Claim Token',
				name: 'claim_token',
				type: 'number',
				default: '={{ $json.claim_token }}',
				required: true,
				description: 'Per-claim fencing token returned by Claim',
				displayOptions: { show: { resource: ['queue'], operation: ['submit'] } },
			},
			{
				displayName: 'Status',
				name: 'status',
				type: 'options',
				options: [
					{ name: 'Matched', value: 'matched' },
					{ name: 'Unmatched', value: 'unmatched' },
					{ name: 'Suppressed', value: 'suppressed' },
				],
				default: 'matched',
				required: true,
				displayOptions: { show: { resource: ['queue'], operation: ['submit'] } },
			},
			{
				displayName: 'Ref',
				name: 'ref',
				type: 'string',
				default: '',
				placeholder: 'tvdb:12345',
				description: 'Opaque canonical ref; required for matched',
				displayOptions: { show: { resource: ['queue'], operation: ['submit'], status: ['matched'] } },
			},
			{
				displayName: 'Confidence',
				name: 'confidence',
				type: 'number',
				typeOptions: { minValue: 0, maxValue: 1, numberPrecision: 3 },
				default: 0.9,
				displayOptions: { show: { resource: ['queue'], operation: ['submit'], status: ['matched'] } },
			},
			{
				displayName: 'Reason',
				name: 'reason',
				type: 'string',
				default: '',
				displayOptions: { show: { resource: ['queue'], operation: ['submit'] } },
			},
		],
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const items = this.getInputData();
		const operation = this.getNodeParameter('operation', 0) as string;

		const credentials = await this.getCredentials(CRED);
		const baseUrl = String(credentials.baseUrl).replace(/\/+$/, '');

		const call = (method: IHttpRequestMethods, path: string, body?: IDataObject) =>
			this.helpers.httpRequest({
				method,
				url: `${baseUrl}${path}`,
				body,
				json: true,
			}) as Promise<IDataObject>;

		if (operation === 'queueStats') {
			const stats = await call('GET', '/queue/stats');
			return [[{ json: stats }]];
		}

		if (operation === 'claim') {
			const res = await call('POST', '/queue/claim', {
				limit: this.getNodeParameter('limit', 0),
				lease_seconds: this.getNodeParameter('lease_seconds', 0),
			});
			const claimed = (res.items as IDataObject[]) ?? [];
			return [claimed.map((r) => ({ json: r }))];
		}

		const out: INodeExecutionData[] = [];
		for (let i = 0; i < items.length; i++) {
			try {
				if (operation === 'ingestPosts') {
					const posts = this.getNodeParameter('posts', i);
					const res = await call('POST', '/ingest', { posts });
					out.push({ json: res, pairedItem: { item: i } });
					continue;
				}

				const body = submitBody(this, i);
				const res = await call('POST', '/submit', body);
				out.push({
					json: { ...items[i].json, takuhai: res },
					pairedItem: { item: i },
				});
			} catch (error) {
				if (this.continueOnFail()) {
					out.push({ json: { error: (error as Error).message }, pairedItem: { item: i } });
					continue;
				}
				throw error;
			}
		}
		return [out];
	}
}

function submitBody(ctx: IExecuteFunctions, i: number): IDataObject {
	const status = ctx.getNodeParameter('status', i) as string;
	const body: IDataObject = {
		infohash: ctx.getNodeParameter('infohash', i),
		claim_token: ctx.getNodeParameter('claim_token', i),
		status,
	};
	if (status === 'matched') {
		body.ref = ctx.getNodeParameter('ref', i);
		body.confidence = ctx.getNodeParameter('confidence', i);
	}
	const reason = ctx.getNodeParameter('reason', i, '') as string;
	if (reason) body.reason = reason;
	return body;
}
