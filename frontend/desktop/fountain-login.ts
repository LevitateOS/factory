import {html} from 'lit';
import {live} from 'lit/directives/live.js';
import {repeat} from 'lit/directives/repeat.js';
import {SodaFactoryRuntimeController} from '../factory/app/browser.js';
import type {Core} from './transport.js';
import type {Account} from './accounts.js';
import {ControllerView, Observed} from './controller-view.js';

interface View {attempt:number;phase:'idle'|'requesting'|'waiting'|'verifying'|'saving'|'complete'|'failed'|'cancelled';prompt:{verification_uri:string;expires_at:number}|null;error:string|null}
interface FountainAccount extends Account {purpose?:'read'|'control'|'issues'|null;grant?:{client_id?:string;scope:string;refresh_available:boolean}}
const purpose=(account:FountainAccount)=>account.purpose==='issues'?'issues only':account.purpose==='control'?'control':account.purpose==='read'?'read':'access unknown';
interface Enrollment {active:boolean;runtime_bound:boolean;repository:{owner:string;name:string;permission:string}}
const idle=():View=>({attempt:0,phase:'idle',prompt:null,error:null});
const unchecked='Check the selected repository’s current enrollment.';

/** Configured-host OAuth stays in Rust; the renderer receives public view state.
 * This controller owns that state, the sign-in and enrollment form values and
 * every request; views only render it. */
export class FountainLogin extends Observed {
  ready=false;busy=false;accountsCurrent=false;
  view:View=idle();error:string|null=null;readError:string|null=null;
  accounts:FountainAccount[]=[];
  origin='';control=false;issues=false;selectedAccount='';repository='';
  enrollment='Enrollment grants repository read access. Execution and publication need separate authorization.';
  private reading=false;private epoch=-1;private interaction=0;private keys='';
  constructor(private readonly core:Pick<Core,'invoke'>){
    super();
    document.addEventListener('visibilitychange',()=>{
      if(document.hidden){this.interaction++;this.accountsCurrent=false;this.notify();}
    });
  }
  get active(){return['requesting','waiting','verifying','saving'].includes(this.view.phase);}
  /** The selected saved account while account status is current. */
  get selected(){return this.accountsCurrent?this.accounts.find(account=>account.connection===this.selectedAccount):undefined;}
  /** Scoped port for the shared runtime controller, or undefined while unusable. */
  controllerContext():SodaFactoryRuntimeController['context']{
    const connection=this.selectedAccount,repository=this.repository.trim(),selected=this.selected;
    if(!this.ready||selected?.state!=='stored'||!connection||!repository)return;
    const canControl=purpose(selected)==='control';
    return {scope:connection+':'+repository.toLowerCase(),generation:String(this.epoch),destination:'this desktop',canSelect:canControl,canAuthorize:canControl,
      request:command=>this.core.invoke('fountain_controller',{connection,repository,...command})};
  }
  private async action(work:(live:()=>boolean)=>Promise<unknown>){
    if(this.busy||!this.ready)return;
    const captured=this.epoch,serial=++this.interaction;
    const live=()=>this.ready&&captured===this.epoch&&serial===this.interaction&&!document.hidden;
    this.busy=true;this.error=null;this.notify();
    try{await work(live);}catch{if(live())this.error='Fountain is unavailable. Your saved accounts are kept.';}
    finally{this.busy=false;if(live())await this.refresh(this.ready,this.epoch);this.notify();}
  }
  setOrigin(value:string){this.origin=value;}
  setControl(checked:boolean){this.control=checked;if(checked)this.issues=false;this.notify();}
  setIssues(checked:boolean){this.issues=checked;if(checked)this.control=false;this.notify();}
  chooseAccount(connection:string){this.selectedAccount=connection;this.enrollment=unchecked;this.notify();}
  setRepository(value:string){this.repository=value;this.enrollment=unchecked;this.notify();}
  signIn(){void this.action(async live=>{const next=await this.core.invoke<View>('fountain_sign_in',{origin:this.origin.trim(),purpose:this.issues?'issues':this.control?'control':'read'});if(live())this.view=next;});}
  open(){void this.action(()=>this.core.invoke('fountain_sign_in_open',{attempt:this.view.attempt}));}
  cancel(){void this.action(async live=>{const next=await this.core.invoke<View>('fountain_sign_in_cancel',{attempt:this.view.attempt});if(live())this.view=next;});}
  refreshLogin(connection:string){void this.action(async live=>{const next=await this.core.invoke<View>('fountain_refresh',{connection});if(live())this.view=next;});}
  remove(connection:string){void this.action(()=>this.core.invoke('credential_withdraw',{connection}));}
  enroll(command:'enroll'|'status'|'withdraw'){void this.action(async live=>{
    const captured=this.epoch,connection=this.selectedAccount,repo=this.repository.trim();
    if(!this.accountsCurrent||!this.accounts.some(account=>account.connection===connection&&account.state==='stored'))return;
    const value=await this.core.invoke<Enrollment>('fountain_enrollment',{connection,repository:repo,action:command});
    if(!live()||captured!==this.epoch||connection!==this.selectedAccount||repo!==this.repository.trim())return;
    this.enrollment=value.active&&value.runtime_bound?value.repository.permission==='issues'?`${value.repository.owner}/${value.repository.name}: issue-only collaboration. Code access, execution and publication are unavailable.`:`${value.repository.owner}/${value.repository.name}: current ${value.repository.permission} enrollment. Execution and publication need separate authorization.`:value.active?'Legacy read enrollment. Sign in again before requesting native control.':'Enrollment withdrawn. Reusing its command will keep it withdrawn.';
  });}
  async refresh(available:boolean,generation:number){
    if(!available&&this.ready)this.interaction++;
    this.ready=available;if(!this.ready)this.accountsCurrent=false;
    if(generation!==this.epoch){this.epoch=generation;this.interaction++;this.accountsCurrent=false;this.view=idle();this.error=null;this.readError=null;}
    if(!this.ready||this.reading){this.notify();return;}this.reading=true;const captured=this.epoch;
    const serial=this.interaction;
    try{
      const [next,saved]=await Promise.all([this.core.invoke<View>('fountain_sign_in_status'),this.core.invoke<FountainAccount[]>('credential_status')]);
      if(captured!==this.epoch||serial!==this.interaction||!this.ready||document.hidden)return;this.view=next;this.readError=null;
      const current=saved.filter(account=>account.provider==='fountain');
      this.accounts=current;this.accountsCurrent=true;
      const keys=JSON.stringify(current.map(account=>[account.connection,account.label,account.account,account.origin,account.state,purpose(account)]));
      if(this.keys!==keys){
        this.keys=keys;
        if(!current.some(account=>account.connection===this.selectedAccount))this.selectedAccount='';
      }
    }catch{if(captured===this.epoch&&serial===this.interaction&&this.ready){this.accountsCurrent=false;this.readError='Saved Fountain account status is unavailable. Your accounts are kept.';}}finally{this.reading=false;this.notify();}
  }
}

const phaseStatus={requesting:'Connecting to Fountain…',waiting:'Approve sign-in in your browser, then return here.',verifying:'Verifying your Fountain account…',saving:'Saving your Fountain account…',complete:'Fountain account saved. Enroll a repository to use its granted access.',cancelled:'Sign-in cancelled.',idle:'Sign in to a Fountain host configured for desktop access.',failed:'Fountain sign-in could not finish. Try again.'} as const;
const describe=(account:FountainAccount)=>`${account.label??account.account} — ${account.origin} (${purpose(account)}, ${account.state})`;

/** Presentation of one FountainLogin controller. Fountain stays available while
 * it is marked unqualified; whether to withhold it is an open owner decision. */
export class SodaDesktopFountain extends ControllerView<FountainLogin> {
  protected render() {
    const c=this.controller;
    if(!c)return html``;
    const active=c.active,locked=!c.ready||c.busy||active;
    const usable=c.selected?.state==='stored';
    const status=!c.ready?'Fountain accounts are unavailable until Factory is ready.':c.readError??c.error??c.view.error??(phaseStatus[c.view.phase]??phaseStatus.idle);
    // Saved-account controls also wait for current account status.
    const rowLocked=!c.ready||!c.accountsCurrent||c.busy||active;
    return html`<p class="soda-factory-unqualified"><strong>Not qualified yet</strong> Most Fountain journeys have not been qualified. Sign-in, repository enrollment and the execution controller below remain available.</p>
      <p id="fountain-status" role="status">${status}</p>
      <form id="fountain-login" @submit=${(event:Event)=>{event.preventDefault();c.signIn();}}>
        <label>Fountain address <input id="fountain-origin" type="url" placeholder="https://fountain.example" required ?disabled=${locked} .value=${live(c.origin)} @input=${(event:Event)=>c.setOrigin((event.target as HTMLInputElement).value)} /></label>
        <label><input id="fountain-control-consent" type="checkbox" ?disabled=${locked} .checked=${live(c.control)} @change=${(event:Event)=>c.setControl((event.target as HTMLInputElement).checked)} /> Request automation control access</label>
        <label><input id="fountain-issues-consent" type="checkbox" ?disabled=${locked} .checked=${live(c.issues)} @change=${(event:Event)=>c.setIssues((event.target as HTMLInputElement).checked)} /> Issue collaboration only</label>
        <p>Issue-only access permits supported issue reads and writes. It grants no code access, execution, PR publication or merge authority.</p>
        <p>Control access permits a separate repository authorization. It does not select a runtime, authorize work or fund inference.</p>
        <button id="fountain-sign-in" type="submit" ?disabled=${locked}>Sign in to Fountain</button>
      </form>
      <button id="fountain-open" type="button" ?hidden=${!c.ready||c.view.phase!=='waiting'||!c.view.prompt} ?disabled=${c.busy} @click=${()=>c.open()}>Continue in browser</button>
      <button id="fountain-cancel" type="button" ?hidden=${!active} ?disabled=${!c.ready||c.busy||c.view.phase==='saving'} @click=${()=>c.cancel()}>Cancel sign-in</button>
      <ul id="fountain-accounts" aria-label="Saved Fountain accounts">${repeat(c.accounts,account=>account.connection,account=>html`<li><span>${describe(account)} </span><button type="button" ?hidden=${!account.grant?.refresh_available} ?disabled=${rowLocked||!['stored','expired'].includes(account.state)} @click=${()=>c.refreshLogin(account.connection)}>Renew sign-in</button><button type="button" ?disabled=${rowLocked} @click=${()=>c.remove(account.connection)}>Remove from Factory</button></li>`)}</ul>
      <form id="fountain-enroll" @submit=${(event:Event)=>{event.preventDefault();c.enroll('enroll');}}>
        <label>Account <select id="fountain-account" aria-label="Fountain account" ?disabled=${rowLocked} @change=${(event:Event)=>c.chooseAccount((event.target as HTMLSelectElement).value)}>
          <option value="" ?selected=${!c.selectedAccount}>Choose a Fountain account</option>
          ${c.accounts.map(account=>html`<option value=${account.connection} ?disabled=${account.state!=='stored'} ?selected=${account.connection===c.selectedAccount}>${describe(account)}</option>`)}
        </select></label>
        <label>Repository <input id="fountain-repository" placeholder="owner/repository" required ?disabled=${rowLocked||!usable} .value=${live(c.repository)} @input=${(event:Event)=>c.setRepository((event.target as HTMLInputElement).value)} /></label>
        <button id="fountain-enroll-submit" type="submit" ?disabled=${!c.ready||!usable||c.busy||active}>Enroll repository</button>
        <button id="fountain-enroll-status" type="button" ?disabled=${!c.ready||!usable||c.busy||active} @click=${()=>c.enroll('status')}>Check enrollment</button>
        <button id="fountain-enroll-withdraw" type="button" ?disabled=${!c.ready||!usable||c.busy||active} @click=${()=>c.enroll('withdraw')}>Withdraw enrollment</button>
      </form>
      <p id="fountain-enrollment" role="status">${c.enrollment}</p>
      <soda-factory-runtime-controller id="fountain-controller" .context=${c.controllerContext()} .disabled=${!c.ready||!usable||c.busy||active}></soda-factory-runtime-controller>`;
  }
}
customElements.define('soda-desktop-fountain', SodaDesktopFountain);
