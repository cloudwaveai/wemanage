import {test} from 'node:test';
import {strict as assert} from 'node:assert';
import {referenceCosts,subscriptionEstimate,convertCNY,costSettings,sumCNY} from '../static/costs.js';
test('three Pro and one Plus produce the full RMB monthly subscription total',()=>{
 const accounts=[1,2,3,4].map(id=>({id,provider:'codex',observed_plan:id===4?'plus':'pro',active:true,members:[]}));
 assert.deepEqual(subscriptionEstimate(accounts,referenceCosts),{cny:4164.42,usd:620,count:4,unknown:0});
 assert.equal(convertCNY(200,'USD',referenceCosts),1343.36);assert.equal(convertCNY(20,'USD',referenceCosts),134.34);
 const custom={...referenceCosts,account_usd:{1:100}};assert.equal(subscriptionEstimate(accounts,custom).usd,520);
 accounts[0].active=false;assert.equal(subscriptionEstimate(accounts,referenceCosts).usd,420);
 accounts[0]={id:1,provider:'anthropic',plan:'pro',active:true};assert.equal(subscriptionEstimate(accounts,referenceCosts).unknown,1);
});
test('monthly FX overrides and real expenses stay separate from subscription estimates',()=>{
 const override={...referenceCosts,rates:{...referenceCosts.rates,USD:7}};
 const sources={cost_settings:{defaults:referenceCosts,months:{'2026-11':{settings:override}}}};
 assert.equal(costSettings(sources,'2026-11').rates.USD,7);assert.equal(costSettings(sources,'2026-10').rates.USD,6.7168);
 assert.deepEqual(sumCNY([{amount:'100',currency:'USD'},{amount:'20',currency:'CNY'}],sources,'2026-11'),{cny:720,unknown:0});
 assert.equal(convertCNY(100,'JPY',referenceCosts),null);
});
