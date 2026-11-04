import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { organizationPeopleFor } from '../static/organization.js';

test('descendants and shared department memberships never duplicate people in one scope',()=>{
 const d={departments:[{id:1,parent_id:null},{id:2,parent_id:1},{id:3,parent_id:1},{id:4,parent_id:null}],members:[{id:10,department_ids:[1,2]},{id:11,department_ids:[2,3]},{id:12,department_ids:[4]},{id:13,department_ids:[]}]};
 assert.deepEqual(organizationPeopleFor(d,1).map(x=>x.id),[10,11]);
 assert.deepEqual(organizationPeopleFor(d,2).map(x=>x.id),[10,11]);
 assert.deepEqual(organizationPeopleFor(d,3).map(x=>x.id),[11]);
 assert.deepEqual(organizationPeopleFor(d,0).map(x=>x.id),[10,11,12,13]);
});
