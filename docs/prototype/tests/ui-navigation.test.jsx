import { test, expect } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { PanelButton } from '../src/ui';

test('panel destinations preserve native links while ordinary clicks navigate in the existing shell', () => {
  const visits=[],element=PanelButton({to:'/chat?channel_id=launch',onView:to=>visits.push(to),children:'Launch'});
  expect(renderToStaticMarkup(element)).toContain('href="#/chat?channel_id=launch"');
  expect(element.type).toBe('a');
  for(const extra of [{ctrlKey:true},{metaKey:true},{shiftKey:true},{altKey:true},{button:1},{button:2},{defaultPrevented:true},{currentTarget:{target:'_blank'}}]) {
    let prevented=false;
    element.props.onClick({button:0,defaultPrevented:false,currentTarget:{target:''},preventDefault:()=>{prevented=true;},...extra});
    expect(prevented).toBe(false);expect(visits).toEqual([]);
  }
  let prevented=false;
  element.props.onClick({button:0,defaultPrevented:false,currentTarget:{target:''},preventDefault:()=>{prevented=true;}});
  expect(prevented).toBe(true);expect(visits).toEqual(['/chat?channel_id=launch']);
});
