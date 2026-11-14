import assert from 'node:assert/strict'
import test from 'node:test'
import { cashRefundActionPath, cashRefundBusinessPath, cashRefundCompleteBody, cashRefundCreateBody, cashRefundExecuteBody, cashRefundReasonBody, cashRefundsPath, parseCashRefund, parseCashRefundPage } from '../src/cash-refunds.ts'

const id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',tenant='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',lot='cccccccc-cccc-4ccc-8ccc-cccccccccccc',maker='dddddddd-dddd-4ddd-8ddd-dddddddddddd',approver='eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee',journal='ffffffff-ffff-4fff-8fff-ffffffffffff',rate='99999999-9999-4999-8999-999999999999',request='88888888-8888-4888-8888-888888888888',at='2026-11-01T00:00:00.123456Z'
const requested=()=>({id,tenant_id:tenant,lot_id:lot,business_ref:'cash-refund:test',coin_amount:null,reason:'verified refund',state:'requested',requested_by:maker,maker,approver:null,transfer_executor:null,fiat_currency:null,fiat_amount:null,buy_rate_version_id:null,refund_rate_version_id:null,single_operator:false,transfer_order_ref:null,transfer_proof_ref:null,paid_fiat_amount:null,hold_journal_id:null,retire_journal_id:null,release_journal_id:null,approved_at:null,created_at:at,request_id:request,single_operator_exception:false,preview_coin_amount:'100',preview_fiat_amount:'90',preview_fiat_currency:'CNY'})
const completed=()=>({...requested(),coin_amount:'100',state:'completed',approver,transfer_executor:maker,fiat_currency:'CNY',fiat_amount:'90',buy_rate_version_id:rate,refund_rate_version_id:rate,transfer_order_ref:'transfer:order',transfer_proof_ref:'transfer:proof',paid_fiat_amount:'90',hold_journal_id:journal,retire_journal_id:journal,approved_at:at,preview_coin_amount:null,preview_fiat_amount:null,preview_fiat_currency:null})

test('cash-refund DTO separates requested preview from completed frozen facts',()=>{
 const before=parseCashRefund(requested());assert.equal(before.preview_coin_amount,'100');assert.equal(before.preview_fiat_amount,'90');assert.equal(before.coin_amount,null);assert.equal(before.approved_at,null)
 const done=parseCashRefund(completed());assert.equal(done.coin_amount,'100');assert.equal(done.fiat_amount,'90');assert.equal(done.paid_fiat_amount,'90');assert.equal(done.retire_journal_id,journal);assert.equal(done.preview_coin_amount,null)
 const listed=parseCashRefundPage([{...completed(),tenant_name:'Customer'}],'');assert.equal(listed.rows[0].tenant_name,'Customer');assert.equal(listed.next,'')
 assert.throws(()=>parseCashRefundPage([completed(),completed()],''));assert.throws(()=>parseCashRefundPage([], 'not-a-cursor'))
})

test('cash-refund DTO rejects amount numbers and invalid state/fact combinations',()=>{
 for(const patch of [
  {coin_amount:100},
  {unknown_field:true},
  {state:'approved'},
  {state:'approved',coin_amount:'100',fiat_amount:'90',approved_at:at,approver,buy_rate_version_id:rate,refund_rate_version_id:rate,fiat_currency:'CNY'},
  {release_journal_id:journal},
  {paid_fiat_amount:'91'},
 ]) assert.throws(()=>parseCashRefund({...completed(),...patch}))
 assert.throws(()=>parseCashRefund({...completed(),state:'cancelled'}))
 assert.throws(()=>parseCashRefund({...completed(),state:'approved',transfer_order_ref:'transfer:early'}))
})

test('cash-refund paths and command bodies forbid caller supplied amounts',()=>{
 assert.equal(cashRefundActionPath(id,'approve'),`/platform/cash-refunds/${id}/approve`);assert.throws(()=>cashRefundActionPath(id,'edit'))
 assert.equal(cashRefundBusinessPath('cash-refund:test'),'/platform/cash-refunds/by-business/cash-refund%3Atest');assert.equal(cashRefundBusinessPath('cash-refund:folder/refund'),'/platform/cash-refunds/by-business/cash-refund%3Afolder%2Frefund')
 assert.equal(cashRefundsPath({tenantID:tenant,state:'completed',businessRef:'cash-refund:test'}),`/platform/cash-refunds?tenant_id=${tenant}&state=completed&business_ref=cash-refund%3Atest`)
 const create=cashRefundCreateBody({tenant_id:tenant,lot_id:lot,business_ref:'cash-refund:test',reason:'verified refund',request_id:''});assert.deepEqual(create,{tenant_id:tenant,lot_id:lot,business_ref:'cash-refund:test',reason:'verified refund',request_id:''})
 assert.throws(()=>cashRefundCreateBody({tenant_id:tenant,lot_id:lot,business_ref:'cash-refund:test',reason:'verified refund',request_id:'',coin_amount:'100'}))
 assert.deepEqual(cashRefundExecuteBody({transfer_order_ref:'transfer:order'}),{transfer_order_ref:'transfer:order'})
 assert.deepEqual(cashRefundCompleteBody({transfer_proof_ref:'transfer:proof',paid_fiat_amount:'90'}),{transfer_proof_ref:'transfer:proof',paid_fiat_amount:'90'})
 assert.deepEqual(cashRefundReasonBody({reason:'bank rejected'}),{reason:'bank rejected'})
 assert.throws(()=>cashRefundCompleteBody({transfer_proof_ref:'transfer:proof',paid_fiat_amount:90}))
})
