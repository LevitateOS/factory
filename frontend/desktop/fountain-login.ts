import type {Core} from './transport.js';
import type {Account} from './accounts.js';
interface View {attempt:number;phase:string;prompt:{verification_uri:string;expires_at:number}|null;error:string|null}
interface FountainAccount extends Account {grant?:{client_id?:string;refresh_available:boolean}}
interface Enrollment {active:boolean;runtime_bound:boolean;repository:{owner:string;name:string;permission:string}}
/** Configured-host OAuth stays in Rust; the renderer receives public view state. */
export function mountFountainLogin(core:Core){
  const form=document.querySelector<HTMLFormElement>('#fountain-login')!;
  const origin=document.querySelector<HTMLInputElement>('#fountain-origin')!;
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
  let selectedAccount='';
  const rows=new Map<string,{row:HTMLLIElement;label:HTMLSpanElement;refresh:HTMLButtonElement;remove:HTMLButtonElement}>();
  let ready=false,reading=false,busy=false,epoch=-1,view:View={attempt:0,phase:'idle',prompt:null,error:null},error:string|null=null,readError:string|null=null;
  const active=()=>['requesting','waiting','verifying','saving'].includes(view.phase);
  function render(){
    begin.disabled=origin.disabled=!ready||busy||active();
    open.hidden=!ready||view.phase!=='waiting'||!view.prompt;open.disabled=busy;
    cancel.hidden=!active();cancel.disabled=busy||view.phase==='saving';
    status.textContent=!ready?'Fountain accounts are unavailable until Factory is ready.':readError??error??view.error??({requesting:'Connecting to Fountain…',waiting:'Approve sign-in in your browser, then return here.',verifying:'Verifying your Fountain account…',saving:'Saving your Fountain account…',complete:'Fountain account saved. Repository enrollment grants read access only.',cancelled:'Sign-in cancelled.'}[view.phase]??'Sign in to a Fountain host configured for desktop access.');
    for(const row of rows.values()){row.refresh.disabled=row.remove.disabled=!ready||busy||active();}
    for(const button of enrollButtons)button.disabled=!ready||busy||active()||!accountSelect.value;
    accountSelect.disabled=repository.disabled=!ready||busy||active();
  }
  async function action(work:()=>Promise<unknown>){if(busy)return;busy=true;error=null;render();try{await work();}catch{error='Fountain is unavailable. Your saved accounts are kept.';}finally{busy=false;await refresh(ready,epoch);render();}}
  form.addEventListener('submit',event=>{event.preventDefault();void action(async()=>{view=await core.invoke<View>('fountain_sign_in',{origin:origin.value.trim()});});});
  open.addEventListener('click',()=>void action(()=>core.invoke('fountain_sign_in_open',{attempt:view.attempt})));
  cancel.addEventListener('click',()=>void action(async()=>{view=await core.invoke<View>('fountain_sign_in_cancel',{attempt:view.attempt});}));
  const enrollmentAction=(command:'enroll'|'status'|'withdraw')=>action(async()=>{
    const captured=epoch,connection=accountSelect.value,repo=repository.value.trim();
    const value=await core.invoke<Enrollment>('fountain_enrollment',{connection,repository:repo,action:command});
    if(captured!==epoch||connection!==accountSelect.value||repo!==repository.value.trim())return;
    enrollment.textContent=value.active&&value.runtime_bound?`${value.repository.owner}/${value.repository.name}: current ${value.repository.permission} enrollment. Execution and publication need separate authorization.`:value.active?'Legacy read enrollment. Sign in again before requesting native control.':'Enrollment withdrawn. Reusing its command will keep it withdrawn.';
  });
  enroll.addEventListener('submit',event=>{event.preventDefault();void enrollmentAction('enroll');});
  enrollButtons[1]!.addEventListener('click',()=>void enrollmentAction('status'));
  enrollButtons[2]!.addEventListener('click',()=>void enrollmentAction('withdraw'));
  accountSelect.addEventListener('change',()=>{selectedAccount=accountSelect.value;enrollment.textContent='Check the selected repository’s current enrollment.';render();});
  async function refresh(available:boolean,generation:number){
    ready=available;if(generation!==epoch){epoch=generation;view={attempt:0,phase:'idle',prompt:null,error:null};error=null;readError=null;}
    if(!ready||reading){render();return;}reading=true;const captured=epoch;
    try{
      const [next,accounts]=await Promise.all([core.invoke<View>('fountain_sign_in_status'),core.invoke<FountainAccount[]>('credential_status')]);
      if(captured!==epoch||!ready)return;view=next;readError=null;
      const current=accounts.filter(account=>account.provider==='fountain');
      const keys=current.map(account=>account.connection).join(',');
      if(accountSelect.dataset.accounts!==keys){accountSelect.replaceChildren(...current.map(account=>new Option(`${account.label??account.account} — ${account.origin}`,account.connection)));accountSelect.dataset.accounts=keys;if(current.some(a=>a.connection===selectedAccount))accountSelect.value=selectedAccount;else selectedAccount=accountSelect.value;}
      for(const [connection,row]of rows)if(!current.some(a=>a.connection===connection)){row.row.remove();rows.delete(connection);}
      for(const account of current){
        let value=rows.get(account.connection);
        if(!value){const row=document.createElement('li'),label=document.createElement('span'),refresh=document.createElement('button'),remove=document.createElement('button');refresh.type=remove.type='button';refresh.textContent='Refresh login';remove.textContent='Remove from Factory';
          refresh.addEventListener('click',()=>void action(async()=>{view=await core.invoke<View>('fountain_refresh',{connection:account.connection});}));
          remove.addEventListener('click',()=>void action(()=>core.invoke('credential_withdraw',{connection:account.connection})));
          row.append(label,refresh,remove);list.append(row);value={row,label,refresh,remove};rows.set(account.connection,value);}
        value.label.textContent=`${account.label??account.account} — ${account.origin} (${account.state}) `;
      }
    }catch{readError='Saved Fountain account status is unavailable. Your accounts are kept.';}finally{reading=false;render();}
  }
  render();return refresh;
}
