import {SodaFactoryRuntimeController} from "../factory/app/browser.js";
import type {Core} from './transport.js';
import type {Account} from './accounts.js';
interface View {attempt:number;phase:'idle'|'requesting'|'waiting'|'verifying'|'saving'|'complete'|'failed'|'cancelled';prompt:{verification_uri:string;expires_at:number}|null;error:string|null}
interface FountainAccount extends Account {grant?:{client_id?:string;scope:string;refresh_available:boolean}}
const purpose=(account:FountainAccount)=>account.grant?.scope==='write:issue'?'issues only':['write:repository','write:repository write:issue'].includes(account.grant?.scope??'')?'control':['read:repository','read:repository read:issue'].includes(account.grant?.scope??'')?'read':'access unknown';
interface Enrollment {active:boolean;runtime_bound:boolean;repository:{owner:string;name:string;permission:string}}
/** Configured-host OAuth stays in Rust; the renderer receives public view state. */
export function mountFountainLogin(core:Core){
  const form=document.querySelector<HTMLFormElement>('#fountain-login')!;
  const origin=document.querySelector<HTMLInputElement>('#fountain-origin')!;
  const control=document.querySelector<HTMLInputElement>('#fountain-control-consent')!;
  const issues=document.querySelector<HTMLInputElement>('#fountain-issues-consent')!;
  const begin=document.querySelector<HTMLButtonElement>('#fountain-sign-in')!;
  const status=document.querySelector<HTMLElement>('#fountain-status')!;
  const open=document.querySelector<HTMLButtonElement>('#fountain-open')!;
  const cancel=document.querySelector<HTMLButtonElement>('#fountain-cancel')!;
  const list=document.querySelector<HTMLUListElement>('#fountain-accounts')!;
  const enroll=document.querySelector<HTMLFormElement>('#fountain-enroll')!;
  const accountSelect=document.querySelector<HTMLSelectElement>('#fountain-account')!;
  const repository=document.querySelector<HTMLInputElement>('#fountain-repository')!;
  const enrollment=document.querySelector<HTMLElement>('#fountain-enrollment')!;
  const enrollButtons=['#fountain-enroll-submit','#fountain-enroll-status','#fountain-enroll-withdraw'].map(id=>document.querySelector<HTMLButtonElement>(id)!);
  const controller=document.querySelector<SodaFactoryRuntimeController>("#fountain-controller")!;
  let selectedAccount='';
  const rows=new Map<string,{row:HTMLLIElement;label:HTMLSpanElement;refresh:HTMLButtonElement;remove:HTMLButtonElement}>();
  let ready=false,reading=false,busy=false,accountsCurrent=false,epoch=-1,interaction=0,view:View={attempt:0,phase:'idle',prompt:null,error:null},error:string|null=null,readError:string|null=null;
  let accounts:FountainAccount[]=[];
  const active=()=>['requesting','waiting','verifying','saving'].includes(view.phase);
  function render(){
    begin.disabled=origin.disabled=!ready||busy||active();
    control.disabled=origin.disabled;
    issues.disabled=origin.disabled;
    open.hidden=!ready||view.phase!=='waiting'||!view.prompt;open.disabled=busy;
    cancel.hidden=!active();cancel.disabled=!ready||busy||view.phase==='saving';
    status.textContent=!ready?'Fountain accounts are unavailable until Factory is ready.':readError??error??view.error??({requesting:'Connecting to Fountain…',waiting:'Approve sign-in in your browser, then return here.',verifying:'Verifying your Fountain account…',saving:'Saving your Fountain account…',complete:'Fountain account saved. Enroll a repository to use its granted access.',cancelled:'Sign-in cancelled.',idle:'Sign in to a Fountain host configured for desktop access.',failed:'Fountain sign-in could not finish. Try again.'}[view.phase]??'Sign in to a Fountain host configured for desktop access.');
    const connection=accountSelect.value,repo=repository.value.trim();
    const selected=accountsCurrent?accounts.find(account=>account.connection===connection):undefined;
    const usable=selected?.state==='stored';
    for(const [connection,row] of rows){
      const account=accounts.find(account=>account.connection===connection);
      row.remove.disabled=!ready||!accountsCurrent||busy||active();
      row.refresh.hidden=!account?.grant?.refresh_available;
      row.refresh.disabled=!ready||!accountsCurrent||busy||active()||!account||!['stored','expired'].includes(account.state);
    }
    for(const button of enrollButtons)button.disabled=!ready||!usable||busy||active();
    accountSelect.disabled=!ready||!accountsCurrent||busy||active();
    repository.disabled=accountSelect.disabled||!usable;
    const canControl=!!selected&&purpose(selected)==='control';
    controller.context=ready&&usable&&connection&&repo?{scope:connection+":"+repo.toLowerCase(),generation:String(epoch),destination:"this desktop",canSelect:canControl,canAuthorize:canControl,request:command=>core.invoke("fountain_controller",{connection,repository:repo,...command})}:undefined;
    controller.disabled=!ready||!usable||busy||active();
  }
  async function action(work:(live:()=>boolean)=>Promise<unknown>){
    if(busy||!ready)return;
    const captured=epoch,serial=++interaction;
    const live=()=>ready&&captured===epoch&&serial===interaction&&!document.hidden;
    busy=true;error=null;render();
    try{await work(live);}catch{if(live())error='Fountain is unavailable. Your saved accounts are kept.';}
    finally{busy=false;if(live())await refresh(ready,epoch);render();}
  }
  control.addEventListener('change',()=>{if(control.checked)issues.checked=false;});
  issues.addEventListener('change',()=>{if(issues.checked)control.checked=false;});
  form.addEventListener('submit',event=>{event.preventDefault();void action(async live=>{const next=await core.invoke<View>('fountain_sign_in',{origin:origin.value.trim(),control:control.checked,issuesOnly:issues.checked});if(live())view=next;});});
  open.addEventListener('click',()=>void action(()=>core.invoke('fountain_sign_in_open',{attempt:view.attempt})));
  cancel.addEventListener('click',()=>void action(async live=>{const next=await core.invoke<View>('fountain_sign_in_cancel',{attempt:view.attempt});if(live())view=next;}));
  const enrollmentAction=(command:'enroll'|'status'|'withdraw')=>action(async live=>{
    const captured=epoch,connection=accountSelect.value,repo=repository.value.trim();
    if(!accountsCurrent||!accounts.some(account=>account.connection===connection&&account.state==='stored'))return;
    const value=await core.invoke<Enrollment>('fountain_enrollment',{connection,repository:repo,action:command});
    if(!live()||captured!==epoch||connection!==accountSelect.value||repo!==repository.value.trim())return;
    enrollment.textContent=value.active&&value.runtime_bound?value.repository.permission==='issues'?`${value.repository.owner}/${value.repository.name}: issue-only collaboration. Code access, execution and publication are unavailable.`:`${value.repository.owner}/${value.repository.name}: current ${value.repository.permission} enrollment. Execution and publication need separate authorization.`:value.active?'Legacy read enrollment. Sign in again before requesting native control.':'Enrollment withdrawn. Reusing its command will keep it withdrawn.';
  });
  enroll.addEventListener('submit',event=>{event.preventDefault();void enrollmentAction('enroll');});
  enrollButtons[1]!.addEventListener('click',()=>void enrollmentAction('status'));
  enrollButtons[2]!.addEventListener('click',()=>void enrollmentAction('withdraw'));
  repository.addEventListener('input',()=>{enrollment.textContent='Check the selected repository’s current enrollment.';render();});
  accountSelect.addEventListener('change',()=>{selectedAccount=accountSelect.value;enrollment.textContent='Check the selected repository’s current enrollment.';render();});
  document.addEventListener('visibilitychange',()=>{
    if(document.hidden){interaction++;accountsCurrent=false;render();}
  });
  async function refresh(available:boolean,generation:number){
    if(!available&&ready)interaction++;
    ready=available;if(!ready)accountsCurrent=false;
    if(generation!==epoch){epoch=generation;interaction++;accountsCurrent=false;view={attempt:0,phase:'idle',prompt:null,error:null};error=null;readError=null;}
    if(!ready||reading){render();return;}reading=true;const captured=epoch;
    const serial=interaction;
    try{
      const [next,saved]=await Promise.all([core.invoke<View>('fountain_sign_in_status'),core.invoke<FountainAccount[]>('credential_status')]);
      if(captured!==epoch||serial!==interaction||!ready||document.hidden)return;view=next;readError=null;
      const current=saved.filter(account=>account.provider==='fountain');
      accounts=current;accountsCurrent=true;
      const keys=JSON.stringify(current.map(account=>[account.connection,account.label,account.account,account.origin,account.state,purpose(account)]));
      if(accountSelect.dataset.accounts!==keys){
        accountSelect.replaceChildren(new Option('Choose a Fountain account',''),...current.map(account=>{
          const option=new Option(`${account.label??account.account} — ${account.origin} (${purpose(account)}, ${account.state})`,account.connection);
          option.disabled=account.state!=='stored';return option;
        }));
        accountSelect.dataset.accounts=keys;
        if(!current.some(account=>account.connection===selectedAccount))selectedAccount='';
        accountSelect.value=selectedAccount;
      }
      for(const [connection,row]of rows)if(!current.some(a=>a.connection===connection)){row.row.remove();rows.delete(connection);}
      for(const account of current){
        let value=rows.get(account.connection);
        if(!value){const row=document.createElement('li'),label=document.createElement('span'),refresh=document.createElement('button'),remove=document.createElement('button');refresh.type=remove.type='button';refresh.textContent='Refresh login';remove.textContent='Remove from Factory';
          refresh.addEventListener('click',()=>void action(async live=>{const next=await core.invoke<View>('fountain_refresh',{connection:account.connection});if(live())view=next;}));
          remove.addEventListener('click',()=>void action(()=>core.invoke('credential_withdraw',{connection:account.connection})));
          row.append(label,refresh,remove);list.append(row);value={row,label,refresh,remove};rows.set(account.connection,value);}
        value.label.textContent=`${account.label??account.account} — ${account.origin} (${purpose(account)}, ${account.state}) `;
      }
    }catch{if(captured===epoch&&serial===interaction&&ready){accountsCurrent=false;readError='Saved Fountain account status is unavailable. Your accounts are kept.';}}finally{reading=false;render();}
  }
  render();return refresh;
}
