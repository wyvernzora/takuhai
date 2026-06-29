import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { LoggerProxy } = require('n8n-workflow');
const { TakuhaiCrawler } = require('../dist/nodes/TakuhaiCrawler/TakuhaiCrawler.node.js');
const { TakuhaiCrawlerTrigger } = require('../dist/nodes/TakuhaiCrawlerTrigger/TakuhaiCrawlerTrigger.node.js');

const logs = [];
LoggerProxy.init({
	debug: (message, meta) => logs.push({ level: 'debug', message, meta }),
	info: (message, meta) => logs.push({ level: 'info', message, meta }),
	warn: (message, meta) => logs.push({ level: 'warn', message, meta }),
	error: (message, meta) => logs.push({ level: 'error', message, meta }),
});

const crawlerRequests = [];
const crawler = new TakuhaiCrawler();
await crawler.execute.call({
	getInputData: () => [{ json: {} }],
	getNodeParameter: (name, _index) =>
		({
			pageSize: 10,
			cursor: '7:10',
			lookback: '24h',
		})[name],
	getCredentials: async () => ({ baseUrl: 'http://crawler.test/' }),
	continueOnFail: () => false,
	helpers: {
		httpRequest: async (request) => {
			crawlerRequests.push(request);
			return { posts: [], next_cursor: '8:0', has_more: true };
		},
	},
});

assert.equal(crawlerRequests[0].method, 'POST');
assert.equal(crawlerRequests[0].url, 'http://crawler.test/crawl');
assert.equal(crawlerRequests[0].body.page_size, 10);
assert.equal(crawlerRequests[0].body.cursor, '7:10');
assert.equal(crawlerRequests[0].body.lookback, '24h');
assert.deepEqual(findLog('Takuhai crawler request started').meta, {
	item_index: 0,
	cursor: '7:10',
	page_size: 10,
	lookback: '24h',
});
assert.equal(findLog('Takuhai crawler page fetched').meta.cursor, '7:10');
assert.equal(findLog('Takuhai crawler page fetched').meta.next_cursor, '8:0');

const state = {};
const requests = [];
const recent = new Date(Date.now() - 60_000).toISOString();
const trigger = new TakuhaiCrawlerTrigger();

const ctx = {
	getWorkflowStaticData: () => state,
	getNodeParameter: (name, fallback) =>
		({
			batchSize: 10,
			pageSize: 10,
			lookback: '1000w',
			options: { resetState: true },
		})[name] ?? fallback,
	getCredentials: async () => ({ baseUrl: 'http://crawler.test/' }),
	helpers: {
		httpRequest: async (request) => {
			requests.push(request);
			return {
				posts: Array.from({ length: 10 }, (_, i) => ({
					source_id: `${request.body.cursor || 'first'}-${i}`,
					published_at: recent,
				})),
				next_cursor: request.body.cursor === '' ? '1:0' : '2:0',
				has_more: true,
			};
		},
	},
};

await trigger.poll.call(ctx);
await trigger.poll.call(ctx);

assert.equal(requests[0].body.cursor, '');
assert.equal(requests[1].body.cursor, '1:0');
assert.equal('lookback' in requests[0].body, false);
assert.equal(findLog('Takuhai crawler trigger response received', { cursor: '' }).meta.next_cursor, '1:0');
assert.equal(findLog('Takuhai crawler trigger response received', { cursor: '1:0' }).meta.next_cursor, '2:0');

console.log('Takuhai crawler node cursor OK');

function findLog(message, meta = {}) {
	const found = logs.find(
		(entry) =>
			entry.message === message &&
			Object.entries(meta).every(([key, value]) => entry.meta?.[key] === value),
	);
	assert.ok(found, `missing log ${message}`);
	return found;
}
