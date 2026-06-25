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
				displayName: 'Body',
				name: 'body',
				type: 'json',
				default: '={{ $json }}',
				required: true,
				description: 'A single /submit body, an array of /submit bodies, or an object with items containing /submit bodies',
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
			return [[{ json: { items: claimed, count: claimed.length } }]];
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

				const payload = submitPayload(this.getNodeParameter('body', i));
				if (payload.batch) {
					const submitted: IDataObject[] = [];
					for (const body of payload.bodies) {
						submitted.push(await call('POST', '/submit', body));
					}
					out.push({
						json: { ...items[i].json, takuhai: { items: submitted, count: submitted.length } },
						pairedItem: { item: i },
					});
					continue;
				}

				const res = await call('POST', '/submit', payload.bodies[0]);
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

function submitPayload(input: unknown): { bodies: IDataObject[]; batch: boolean } {
	if (typeof input === 'string') {
		return submitPayload(JSON.parse(input));
	}
	if (Array.isArray(input)) {
		return { bodies: input.map(asSubmitObject), batch: true };
	}
	const body = asSubmitObject(input);
	const batch = body.items;
	if (Array.isArray(batch)) {
		return { bodies: batch.map(asSubmitObject), batch: true };
	}
	return { bodies: [body], batch: false };
}

function asSubmitObject(input: unknown): IDataObject {
	if (input && typeof input === 'object' && !Array.isArray(input)) {
		return input as IDataObject;
	}
	throw new Error('Submit body must be an object, an array of objects, or an object with items');
}
