import { paymentUnknownWrite } from './payments-transport.ts'
import type { PaymentPending,PaymentReceipt } from './payments.ts'
export type PaymentPhase='idle'|'journaled'|'submitting'|'settled'|'unknown'|'known_failure'
export interface PaymentReview{phase:PaymentPhase;pending:PaymentPending|null;receipt:PaymentReceipt|null;cleanupBlocked:boolean}
export const emptyPaymentReview=():PaymentReview=>({phase:'idle',pending:null,receipt:null,cleanupBlocked:false})
export const resumePaymentReview=(pending:PaymentPending):PaymentReview=>({phase:'unknown',pending,receipt:null,cleanupBlocked:true})
export function failPaymentReview(review:PaymentReview,cause:unknown):PaymentReview{if(!review.pending||!['submitting','journaled'].includes(review.phase))throw new Error('原操作不处于提交状态。');return {...review,phase:paymentUnknownWrite(cause)?'unknown':'known_failure',cleanupBlocked:true}}
export function paymentPollDelay(attempt:number):number|null{return [1000,2000,5000,10000,20000][attempt]??null}
export interface PaymentTicket{readonly epoch:number;readonly lane:string;readonly sequence:number;readonly signal:AbortSignal}
export function createPaymentEpoch(){let epoch=0,context='',disposed=false;const lanes=new Map<string,{sequence:number;controller:AbortController}>();let sequence=0
 function cancel(lane:string){lanes.get(lane)?.controller.abort();lanes.delete(lane)}
 function reset(){for(const lane of lanes.keys())cancel(lane);epoch++}
 return {auth(next:string){if(next!==context){context=next;reset()}},ticket(lane:string):PaymentTicket{cancel(lane);const controller=new AbortController();const entry={sequence:++sequence,controller};lanes.set(lane,entry);if(disposed)controller.abort();return Object.freeze({epoch,lane,sequence:entry.sequence,signal:controller.signal})},accepts(ticket:PaymentTicket){return !disposed&&!ticket.signal.aborted&&ticket.epoch===epoch&&lanes.get(ticket.lane)?.sequence===ticket.sequence},cancel,expire(ticket:PaymentTicket){if(disposed||ticket.epoch!==epoch||lanes.get(ticket.lane)?.sequence!==ticket.sequence)return false;cancel(ticket.lane);return true},dispose(){disposed=true;context='';reset()}}
}
export function newPaymentPending(kind:PaymentPending['kind'],scope:string,target:string,operation=crypto.randomUUID()):PaymentPending{return {operation_id:operation,kind,scope,target_id:target,created_at:new Date().toISOString().replace(/\.(\d{3})Z$/,(_all,micro:string)=>`.${micro}000Z`)}}
export function paymentPageCursor(next:string|null,current:string|null,seen:readonly string[]):string|null{if(next!==null&&(next===current||seen.includes(next)))throw new Error('分页位置没有前进，请刷新第一页。');return next}
