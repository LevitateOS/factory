import {LitElement, html} from 'lit';
import {PendingRecord} from '../shared/retained-record.js';

interface Placement {repository_id:string;runtime_id:string;owner_kind:string;owner_id:string;generation:string;revision:string;state:string}
interface Selection {command_id:string;expected_revision:string;action:string;not_after:number}
interface Authorization {command_id:string;expected_placement:string;generation:string;operations:string[];not_after:number;actor_binding?:string;policy_revision?:number}
interface Command {selection?:Selection;authorization?:Authorization}
export interface ControllerContext {
  scope:string;
  generation:string;
  destination:string;
  canSelect?:boolean;
  canAuthorize?:boolean;
  actors?:{id:string;operations:string[]}[];
  policyRevision?:number;
  request(command:Command):Promise<unknown>;
}
const operations=['execution.launch','git.ref.publish','issue.comment.publish','issue.graph.mutate','issue.lifecycle.apply','pull_request.create','pull_request.merge','pull_request.review.request','pull_request.review.submit','pull_request.review.thread.resolve'];
const id=(value:unknown):value is string=>typeof value==='string'&&/^[a-f0-9]{32}$/.test(value);
const row=(value:unknown):Record<string,unknown>|undefined=>value!==null&&typeof value==='object'&&!Array.isArray(value)?value as Record<string,unknown>:undefined;
function placement(value:unknown):Placement {
  const r=row(value);
  if(!r||typeof r.repository_id!=='string'||!/^\d+$/.test(r.repository_id)||typeof r.generation!=='string'||!/^\d+$/.test(r.generation)||
    typeof r.runtime_id!=='string'||typeof r.owner_kind!=='string'||typeof r.owner_id!=='string'||
    typeof r.revision!=='string'||(r.revision!==''&&!id(r.revision))||!['active','retiring','inactive'].includes(String(r.state)))throw Error('controller response');
  return r as unknown as Placement;
}
function command(value:unknown):Command|undefined {
  const r=row(value);if(!r||Object.keys(r).length!==1)return;
  const selection=row(r.selection),authorization=row(r.authorization),request=selection??authorization;
  if(!request||!id(request.command_id)||!Number.isSafeInteger(request.not_after)||Number(request.not_after)<=0)return;
  if(selection&&Object.keys(selection).length===4&&['activate','withdraw','retire'].includes(String(selection.action))&&
    (selection.expected_revision===''||id(selection.expected_revision)))return r as Command;
  if(authorization&&Object.keys(authorization).length===(authorization.actor_binding===undefined?5:6)+(authorization.policy_revision===undefined?0:1)&&(authorization.policy_revision===undefined||authorization.actor_binding!==undefined&&Number.isSafeInteger(authorization.policy_revision)&&Number(authorization.policy_revision)>0)&&id(authorization.expected_placement)&&typeof authorization.generation==='string'&&/^[1-9]\d*$/.test(authorization.generation)&&
    Array.isArray(authorization.operations)&&(authorization.actor_binding===undefined?JSON.stringify(authorization.operations)===JSON.stringify(operations):typeof authorization.actor_binding==='string'&&/^[1-9]\d*:[1-9]\d*$/.test(authorization.actor_binding)&&authorization.operations.length>0&&authorization.operations.length<=10&&authorization.operations.every(x=>operations.includes(x))&&new Set(authorization.operations).size===authorization.operations.length))return r as Command;
  return;
}

/** Shared presentation; each authenticated consumer supplies its scoped host
 * adapter. Runtime selection, operation grants and inference funding remain
 * independent decisions. The host owns every admission and durable receipt. */
export class SodaFactoryRuntimeController extends LitElement {
  static properties={disabled:{type:Boolean},busy:{state:true},message:{state:true},current:{state:true},pending:{state:true},blocked:{state:true},actorBinding:{state:true},selectForWork:{state:true}};
  private actorBinding='';
  private selectForWork=false;
  declare disabled:boolean;
  declare private busy:boolean;
  declare private message:string;
  declare private current:Placement|undefined;
  declare private pending:Command|undefined;
  declare private blocked:boolean;
  private selected:ControllerContext|undefined;
  private record:PendingRecord<Command>|undefined;
  private serial=0;
  constructor(){super();this.disabled=this.busy=this.blocked=false;this.message='Check the selected repository before authorizing control.';}
  protected createRenderRoot(){return this;}
  set context(value:ControllerContext|undefined){
    const prior=this.selected;this.selected=value;this.requestUpdate();
    if(prior?.scope===value?.scope&&prior?.generation===value?.generation)return;
    this.serial++;this.busy=false;this.current=undefined;this.pending=undefined;this.record=undefined;this.blocked=false;this.actorBinding='';this.selectForWork=false;
    this.message='Check the selected repository before authorizing control.';
    if(!value)return;
    if(!value.scope||value.scope.length>1024||/[\u0000-\u001f\u007f]/.test(value.scope)){this.blocked=true;return;}
    this.record=new PendingRecord(`fountain-controller:${value.scope}`,4096,raw=>command(JSON.parse(raw)),JSON.stringify,(a,b)=>JSON.stringify(a)===JSON.stringify(b));
    this.recover();
  }
  private recover(){
    const saved=this.record?.read();this.pending=saved?.pending;this.blocked=saved?.blocked??false;
    if(this.blocked)this.message='The saved controller command is unavailable. Its contents are preserved; no command was sent.';
  }
  disconnectedCallback(){super.disconnectedCallback();this.serial++;this.busy=false;}
  private async send(input:Command){
    const context=this.selected,record=this.record,serial=this.serial;
    if(!context||!record||this.disabled||this.busy||this.blocked)return;
    if((input.selection||input.authorization)&&(!command(input)||record.retain(input)!=='saved')){this.recover();this.message='The saved command changed. Its contents are preserved; no request was sent.';return;}
    this.busy=true;
    try {
      const value=row(await context.request(input));
      if(serial!==this.serial)return;
      if(!value)throw Error('controller response');
      if(input.selection){
        if(value.command_id!==input.selection.command_id||!['applied','refused'].includes(String(value.outcome)))throw Error('command changed');
        this.current=placement(value.placement);
        this.message=value.outcome==='refused'?`Command refused (${String(value.reason_code??'controller changed')}). Its refusal is retained.`:`Controller ${this.current.state}, generation ${this.current.generation}.`;
      }else if(input.authorization){
        if(value.id!==input.authorization.command_id||value.not_after!==input.authorization.not_after||typeof value.active!=='boolean')throw Error('grant changed');
        this.message=value.selection_outcome==='refused'?'The original authorization is retained. Repository policy changed or the grant became inactive, so it was not selected for new work.':value.active?`Operation grant ${input.authorization.command_id} authorized until ${new Date(input.authorization.not_after*1000).toLocaleTimeString()}. Funding remains separate.`:'The original operation grant is inactive. It was not renewed.';
      }else{this.current=placement(value);this.message=`Controller ${this.current.state}${this.current.owner_kind?` on ${this.current.owner_kind}`:''}, generation ${this.current.generation}.`;}
      if(input.selection||input.authorization)record.settle(input);
      this.recover();
    }catch{if(serial===this.serial)this.message='Controller access is unavailable. Any saved command keeps its original identity and deadline.';}
    finally{if(serial===this.serial)this.busy=false;}
  }
  private async issue(action:string){
    const current=this.current,record=this.record;
    if(!current||!record||this.disabled||this.busy||this.selected?.canSelect===false||action==='authorize'&&this.selected?.canAuthorize===false)return;
    this.recover();if(this.blocked||this.pending)return;
    const actor=this.selected?.actors?.find(a=>a.id===this.actorBinding);
    if(action==='authorize'&&this.selected?.actors&&!actor){this.message='Choose the enrolled actor to authorize.';return;}
    const command_id=crypto.randomUUID().replaceAll('-',''),now=Math.floor(Date.now()/1000);
    const next:Command=action==='authorize'?{authorization:{command_id,expected_placement:current.revision,generation:current.generation,operations:actor?.operations??operations,not_after:now+900,...(actor?{actor_binding:actor.id}:{}),...(actor&&this.selectForWork&&this.selected?.policyRevision!==undefined?{policy_revision:this.selected.policyRevision}:{})}}:
      {selection:{command_id,expected_revision:current.revision,action,not_after:now+300}};
    if(record.retain(next)!=='saved'){this.recover();this.message='The command could not be saved. No request was sent.';return;}
    this.recover();await this.send(next);
  }
  render(){
    const unavailable=!this.selected||this.disabled||this.busy||this.blocked;
    const held=unavailable||!!this.pending||!this.current||this.selected?.canSelect===false;
    return html`<fieldset><legend>Repository controller</legend>
      <p>Selecting a runtime requires repository administration. A 15 minute operation grant covers execution, publication, issues, reviews and allowed merge. Funding remains separate.</p>
      <button id="fountain-controller-status" type="button" ?disabled=${unavailable} @click=${()=>this.send({})}>Check controller</button>
      <button id="fountain-controller-activate" type="button" ?disabled=${held||this.current?.state!=='inactive'} @click=${()=>this.issue('activate')}>Select ${this.selected?.destination??'runtime'}</button>
      ${this.selected?.actors?html`<label>Enrolled operation actor <select id="fountain-controller-actor" ?disabled=${held} .value=${this.actorBinding} @change=${(event:Event)=>{this.actorBinding=(event.target as HTMLSelectElement).value;}}><option value="">Choose an actor</option>${this.selected.actors.map(actor=>html`<option value=${actor.id}>${actor.id} — ${actor.operations.join(', ')}</option>`)}</select></label>`:null}
      ${this.selected?.policyRevision!==undefined?html`<label><input id="fountain-controller-use-for-work" type="checkbox" ?disabled=${held} .checked=${this.selectForWork} @change=${(event:Event)=>{this.selectForWork=(event.target as HTMLInputElement).checked;}}>Select this authorization for new repository work. Funding remains separate.</label>`:null}
      <button id="fountain-controller-authorize" type="button" ?disabled=${held||this.current?.state!=='active'||this.selected?.canAuthorize===false} @click=${()=>this.issue('authorize')}>Authorize operations for 15 minutes</button>
      ${this.selected?.canAuthorize===false?html`<p>Operation authorization is unavailable for this runtime.</p>`:null}
      <button id="fountain-controller-withdraw" type="button" ?disabled=${held||this.current?.state!=='active'} @click=${()=>this.issue('withdraw')}>Withdraw controller</button>
      <button id="fountain-controller-retire" type="button" ?disabled=${held||this.current?.state!=='retiring'} @click=${()=>this.issue('retire')}>Finish retirement</button>
      <button id="fountain-controller-retry" type="button" ?hidden=${!this.pending} ?disabled=${unavailable} @click=${()=>this.pending&&this.send(this.pending)}>Retry saved command</button>
      <p id="fountain-controller-result" role="status">${this.message}</p></fieldset>`;
  }
}
if(!customElements.get('soda-factory-runtime-controller'))customElements.define('soda-factory-runtime-controller',SodaFactoryRuntimeController);
