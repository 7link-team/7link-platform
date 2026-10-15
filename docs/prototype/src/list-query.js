// Callers provide the complete currently authorized collection, never a page or a raw tenant store.
const collator = new Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" });
const empty = value => value == null || value === "" || typeof value === "number" && !Number.isFinite(value) || value instanceof Date && !Number.isFinite(value.getTime());
const integer = (value, fallback) => /^(0|[1-9]\d*)$/.test(String(value)) && Number.isSafeInteger(Number(value)) ? Number(value) : fallback;
export const defaultRowId = row => row.id;
export function listRowId(row, getRowId = defaultRowId) {
  const id = getRowId(row);
  if (!["string", "number"].includes(typeof id) || !String(id).trim() || typeof id === "number" && !Number.isFinite(id)) throw Error("列表记录缺少稳定 ID，不能排序或导出。");
  return String(id);
}
export function queryList(rows, { columns = [], getRowId = defaultRowId, searchText, filterRow, query = "", sort = "", order = "asc", page = 1, pageSize = 20, pageSizes = [10,20,50,100] } = {}) {
  if (!Array.isArray(rows)) throw Error("列表数据须为当前授权记录数组。");
  if (!pageSizes.length || pageSizes.some(size => !Number.isSafeInteger(size) || size < 1)) throw Error("页大小配置须为正整数。");
  const ids = new Set();
  const indexed = rows.map(row => { const id = listRowId(row,getRowId); if (ids.has(id)) throw Error(`列表稳定 ID 重复：${id}`); ids.add(id); return { row,id }; });
  const needle = typeof query === "string" ? query.trim().toLocaleLowerCase() : "";
  const sortable = columns.filter(column => column.sortable !== false && typeof (column.sortValue || column.value) === "function");
  const column = sortable.find(column => column.key === sort);
  const direction = order === "desc" ? "desc" : "asc";
  const matching = indexed.filter(({row}) => (!filterRow || filterRow(row)) && (!needle || String(searchText ? searchText(row) : columns.map(column => column.value?.(row) ?? "").join(" ")).toLocaleLowerCase().includes(needle)));
  matching.sort((a,b) => {
    let compared = 0;
    if (column) {
      const value = column.sortValue || column.value, left = value(a.row), right = value(b.row), leftEmpty = empty(left), rightEmpty = empty(right);
      if (leftEmpty !== rightEmpty) return leftEmpty ? 1 : -1;
      if (!leftEmpty) {
        const x = left instanceof Date ? left.getTime() : left, y = right instanceof Date ? right.getTime() : right;
        compared = typeof x === "number" && typeof y === "number" ? Math.sign(x-y) : typeof x === "boolean" && typeof y === "boolean" ? Number(x)-Number(y) : collator.compare(String(x),String(y));
        if (compared) return direction === "desc" ? -compared : compared;
      }
    }
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
  const requestedSize = integer(pageSize,pageSizes[0]), size = pageSizes.includes(requestedSize) ? requestedSize : pageSizes[0];
  const count = matching.length, pageCount = Math.max(1,Math.ceil(count/size)), currentPage = Math.min(pageCount,Math.max(1,integer(page,1))), offset = (currentPage-1)*size;
  return { rows:matching.slice(offset,offset+size).map(item=>item.row), matchedRows:matching.map(item=>item.row), count, authorizedCount:rows.length, page:currentPage, pageSize:size, pageCount, offset, sort:column?.key || "", order:direction };
}
function jsonValue(value, seen = new Set(), forbidden = new Set()) {
  if (forbidden.has(value)) throw Error("不能直接导出原始记录对象，请逐列声明可读字段。");
  if (value === null || ["string","boolean"].includes(typeof value)) return value;
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (value instanceof Date && Number.isFinite(value.getTime())) return value.toISOString();
  if (!value || typeof value !== "object" || seen.has(value)) throw Error("导出字段包含不可序列化值；未生成文件。");
  seen.add(value);
  let result;
  if (Array.isArray(value)) result = value.map(item=>jsonValue(item,seen,forbidden));
  else {
    if (![Object.prototype,null].includes(Object.getPrototypeOf(value))) throw Error("导出字段须为明确的 JSON 值。");
    result = Object.fromEntries(Object.entries(value).map(([key,item])=>[key,jsonValue(item,seen,forbidden)]));
  }
  seen.delete(value);return result;
}
// Only exportValue explicitly opts a field in. No original row or spread of a row is exported.
export function projectListExport(rows, columns, { getRowId = defaultRowId, source, query = "", sort = "", order = "asc", scopeLabel = "当前查询结果" } = {}) {
  if (typeof source !== "string" || !source.trim()) throw Error("导出须声明列表来源。");
  const allowed = columns.filter(column=>typeof column.exportValue === "function");
  if (!allowed.length) throw Error("此列表未配置可导出字段。");
  if (allowed.some(column=>typeof column.key !== "string" || !column.key || column.key === "id") || new Set(allowed.map(column=>column.key)).size !== allowed.length) throw Error("导出列须使用唯一字段名；稳定 ID 由列表统一提供。");
  const ids = new Set();
  const forbidden = new Set(rows);
  const records = rows.map(row=>{
    const id=listRowId(row,getRowId);if(ids.has(id))throw Error("导出记录 ID 重复。");ids.add(id);
    const fields=Object.fromEntries(allowed.map(column=>[column.key,jsonValue(column.exportValue(row) ?? null,new Set(),forbidden)]));
    return {id,version:null,...fields};
  });
  return { source:source.trim(), scope:scopeLabel, query:String(query), sort, order, count:records.length, versionNote:"version 为 null 表示该列表未提供版本，不推测或补造。", records };
}
