import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { clientworkApi, type PackageBundleState } from "../../lib/clientwork-api"



export function PackageBundle({project,id,editable,onChanged,mutate,onState}:{project:string;id:string;editable:boolean;onChanged:()=>void;onState?:(state:PackageBundleState["state"])=>void;mutate:(body:unknown,key:string,action:string)=>Promise<void>}){
 const t=useTranslations("iam")
 const [bundle,setBundle]=useState<PackageBundleState>(),[error,setError]=useState(false),[busy,setBusy]=useState(false)
 useEffect(()=>{const controller=new AbortController();let timer:ReturnType<typeof setTimeout>|undefined
  const read=async()=>{try{const row=await clientworkApi.packageBundle(project,id,controller.signal);if(controller.signal.aborted)return;setBundle(row);onState?.(row.state);setError(false);if(row.state==="BUILDING")timer=setTimeout(()=>void read(),2000)}catch{if(!controller.signal.aborted)setError(true)}}
  void read();return()=>{controller.abort();clearTimeout(timer)}
 },[project,id,onChanged,busy,onState])
 const build=async()=>{setBusy(true);setError(false);try{await mutate({},crypto.randomUUID(),"bundles");onChanged()}catch{setError(true)}finally{setBusy(false)}}
 const download=async()=>{setBusy(true);try{const file=await clientworkApi.packageBundleContent(project,id);const url=URL.createObjectURL(file),anchor=document.createElement("a");anchor.href=url;anchor.download="delivery.zip";anchor.click();setTimeout(()=>URL.revokeObjectURL(url),0)}catch{setError(true)}finally{setBusy(false)}}
 return <section className="grid min-w-0 gap-2 border-t py-3" aria-label={t("packageBundleTitle")}><h4 className="font-medium">{t("packageBundleTitle")}</h4><p className="text-sm text-muted-foreground">{t("packageBundleHint")}</p><p role="status" aria-atomic="true">{bundle?t(`packageBundleState${bundle.state}`):"…"}</p>{bundle?.fingerprint&&<p className="break-all text-xs">{t("packageBundleFingerprint")}: {bundle.fingerprint}</p>}{bundle?.bundle_sha256&&<p className="break-all text-xs">{t("packageBundleZip")}: {bundle.bundle_sha256}</p>}{bundle?.manifest_sha256&&<p className="break-all text-xs">{t("packageBundleManifest")}: {bundle.manifest_sha256}</p>}{bundle?.error_code&&<p role="alert">{bundle.error_code}</p>}{error&&<p role="alert">{t("packageBundleError")}</p>}<div className="flex flex-wrap gap-2">{editable&&<Button variant="outline" disabled={busy||bundle?.state==="BUILDING"} onClick={()=>void build()}>{t("packageBundleBuild")}</Button>}{bundle?.state==="BUILT"&&<Button variant="outline" disabled={busy} onClick={()=>void download()}>{t("packageBundleDownload")}</Button>}</div></section>
}
