import type {
	IDataObject,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
	IPollFunctions,
} from 'n8n-workflow';

const CRED = 'takuhaiApi';

/**
 * Takuhai Trigger — polls by claiming queue work. It stays idle when no work is
 * claimable and emits one item per claimed release when work exists. The poll schedule
 * comes from n8n's standard polling UI.
 */
export class TakuhaiTrigger implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'Takuhai Trigger',
		name: 'takuhaiTrigger',
		icon: 'file:takuhai.svg',
		group: ['trigger'],
		version: 1,
		subtitle: '={{"claim: " + $parameter["limit"]}}',
		description: 'Claims takuhai work when releases are available',
		defaults: { name: 'Takuhai Trigger' },
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

		const res = (await this.helpers.httpRequest({
			method: 'POST',
			url: `${baseUrl}/queue/claim`,
			body: {
				limit: this.getNodeParameter('limit', 10),
				lease_seconds: this.getNodeParameter('lease_seconds', 300),
			},
			json: true,
		})) as IDataObject;

		const claimed = (res.items as IDataObject[]) ?? [];
		if (claimed.length === 0) {
			return null;
		}

		return [this.helpers.returnJsonArray(claimed)];
	}
}
