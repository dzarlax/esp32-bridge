import { useEffect, useRef, useState } from "react";
import { AdminApi } from "./api";
import { useT } from "./i18n";
export interface CalendarDetails { date: string; complete: boolean; reason: string; sources: {id:string;state:string;stale?:boolean;reason:string}[] }
const api=new AdminApi();
const messages:Record<string,string>={complete:"Расписание полное",ready:"Данные актуальны",cache_stale:"Данные календаря ожидают обновления",source_incomplete:"Данные источника неполные",source_error:"Источник сообщил об ошибке",source_pending:"Источник ещё не готов",incomplete:"Расписание может быть неполным",unknown:"Причина неполноты неизвестна",disabled:"Календарь выключен"};
export function CalendarStatus({deviceId}:{deviceId:string}) {
 const t=useT();const [details,setDetails]=useState<CalendarDetails|null>(null),[names,setNames]=useState<Record<string,string>>({}),[busy,setBusy]=useState(false),[failed,setFailed]=useState(false);
 const request=useRef<AbortController|null>(null);
 useEffect(()=>()=>request.current?.abort(),[]);
 async function refresh(){
  request.current?.abort();const controller=new AbortController();request.current=controller;setBusy(true);setFailed(false);
  try {
   const data=await api.calendarStatus(deviceId,controller.signal);
   if(controller.signal.aborted)return;
   if(!Array.isArray(data.sources)||typeof data.complete!=="boolean")throw Error("invalid response");
   setDetails(data);
   try{const catalog=await api.catalog(deviceId,"calendars");if(!controller.signal.aborted)setNames(Object.fromEntries(catalog.items.map(i=>[i.id,i.name])));}catch{/* Status remains useful if the catalog is temporarily unavailable. */}
  }catch{if(!controller.signal.aborted)setFailed(true);}
  finally{if(!controller.signal.aborted)setBusy(false);}
 }
 return <div className="status-card">
  <p>{t("Состояние сохранённых календарей")}</p>
  <button className="button secondary" type="button" disabled={busy} onClick={()=>void refresh()}>{t(busy?"Проверяю календари…":"Проверить календари")}</button>
  {failed&&<p role="alert">{t("Не удалось проверить календари. Повторите запрос.")}</p>}
  {details&&<div aria-live="polite"><p>{t(messages[details.reason]||messages.unknown)}{details.date?` · ${details.date}`:""}</p>
  <ul>{details.sources.map((source,index)=><li key={source.id}>{names[source.id]||`${t("Календарь")} ${index+1}`} — {t(messages[source.reason]||messages.unknown)}</li>)}</ul>
  <small>{t("Статусы относятся к сохранённому выбору. Обновление страницы не запускает синхронизацию провайдера.")}</small></div>}
 </div>;
}
