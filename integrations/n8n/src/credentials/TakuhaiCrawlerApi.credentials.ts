import type { Icon, ICredentialType, INodeProperties } from 'n8n-workflow';

/**
 * Credentials for a takuhai-shaped crawler — ANY source exposing the shared POST /crawl
 * contract (DMHY first). Each crawler is a separate, stateless service with its own base
 * URL, so point a distinct credential at each crawler deployment.
 */
export class TakuhaiCrawlerApi implements ICredentialType {
	name = 'takuhaiCrawlerApi';
	displayName = 'Takuhai Crawler API';
	documentationUrl = 'https://github.com/wyvernzora/takuhai';
	// Same cropped n8n asset as the nodes; copy-assets.mjs places
	// takuhai.svg next to the compiled credential, where n8n resolves file: icons.
	icon: Icon = 'file:takuhai.svg';

	properties: INodeProperties[] = [
		{
			displayName: 'Base URL',
			name: 'baseUrl',
			type: 'string',
			default: 'http://takuhai-crawler-dmhy:8080',
			placeholder: 'http://takuhai-crawler-<source>:8080',
			required: true,
			description: 'Base URL of a takuhai-shaped crawler (any source exposing POST /crawl)',
		},
	];
}
