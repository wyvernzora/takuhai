import type {
	Icon,
	ICredentialTestRequest,
	ICredentialType,
	INodeProperties,
} from 'n8n-workflow';

/**
 * Credentials for the takuhai service (REST catalog, ingest, and queue surfaces).
 * takuhai enforces no application-level auth by design, so this is just a base URL.
 */
export class TakuhaiApi implements ICredentialType {
	name = 'takuhaiApi';
	displayName = 'Takuhai API';
	documentationUrl = 'https://github.com/wyvernzora/takuhai';
	icon: Icon = 'file:takuhai.svg';

	properties: INodeProperties[] = [
		{
			displayName: 'Base URL',
			name: 'baseUrl',
			type: 'string',
			default: 'http://takuhai:8080',
			placeholder: 'http://takuhai:8080',
			required: true,
			description: 'Base URL of the takuhai service',
		},
	];

	test: ICredentialTestRequest = {
		request: {
			baseURL: '={{$credentials.baseUrl}}',
			url: '/healthz',
			method: 'GET',
		},
	};
}
