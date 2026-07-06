import type {
	IDataObject,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
	IPollFunctions,
} from 'n8n-workflow';
import { LoggerProxy as Logger } from 'n8n-workflow';

const CRED = 'takuhaiApi';

/**
 * Takuhai Queue Trigger — polls by claiming queue work. It stays idle when no work is
 * claimable and emits one batch item when work exists. The poll schedule comes from
 * n8n's standard polling UI.
 */
export class TakuhaiTrigger implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'Takuhai Queue Trigger',
		name: 'takuhaiTrigger',
		icon: 'file:takuhai.svg',
		group: ['trigger'],
		version: 1,
		subtitle: '={{"claim: " + $parameter["limit"]}}',
		description: 'Claims takuhai queue work when releases are available',
		codex: {
			categories: ['Core Nodes'],
			subcategories: {
				'Core Nodes': ['Helpers'],
			},
		},
		defaults: { name: 'Takuhai Queue Trigger' },
		polling: true,
		inputs: [],
		outputs: ['main'],
		credentials: [{ name: CRED, required: true }],
		properties: [
			{
				displayName: 'Limit',
				name: 'limit',
				type: 'number',
				typeOptions: { minValue: 1 },
				default: 10,
				description: 'Max releases to claim per poll',
			},
			{
				displayName: 'Lease Seconds',
				name: 'lease_seconds',
				type: 'number',
				default: 300,
				description: 'Lease length; honored if supplied, else a server default',
			},
		],
	};

	async poll(this: IPollFunctions): Promise<INodeExecutionData[][] | null> {
		const credentials = await this.getCredentials(CRED);
		const baseUrl = String(credentials.baseUrl).replace(/\/+$/, '');

		let res: IDataObject;
		try {
			res = (await this.helpers.httpRequest({
				method: 'POST',
				url: `${baseUrl}/queue/claim`,
				body: {
					limit: this.getNodeParameter('limit', 10),
					lease_seconds: this.getNodeParameter('lease_seconds', 300),
				},
				json: true,
			})) as IDataObject;
		} catch (error) {
			Logger.debug('Takuhai queue trigger claim failed', { err: (error as Error).message });
			throw error;
		}

		const claimed = (res.items as IDataObject[]) ?? [];
		if (claimed.length === 0) {
			Logger.debug('Takuhai queue trigger poll completed with no claims');
			return null;
		}

		Logger.info('Takuhai queue trigger emitted claims', { claimed_count: claimed.length });
		return [[{ json: { items: claimed, count: claimed.length } }]];
	}
}
