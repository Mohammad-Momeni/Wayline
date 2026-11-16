package io.routeweave.app;

import android.app.*;
import android.content.*;
import android.content.pm.*;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.text.*;
import android.view.*;
import android.widget.*;
import java.util.*;

/** Searchable icon/name/package picker shared by routing and proxy selection. */
final class AppPicker {
    interface Selection { void apply(LinkedHashSet<String> packages); }
    static final class Entry { String name, id; Drawable icon; Entry(String name,String id,Drawable icon){this.name=name;this.id=id;this.icon=icon;} }
    static void show(Activity activity,String title,Set<String> initial,boolean multiple,Selection result){
        PackageManager manager=activity.getPackageManager();LinkedHashMap<String,Entry> inventory=new LinkedHashMap<>();
        for(ResolveInfo info:manager.queryIntentActivities(new Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER),0)){
            String id=info.activityInfo.packageName;if(id.equals(activity.getPackageName()))continue;inventory.put(id,new Entry(info.loadLabel(manager).toString(),id,null));
        }
        for(String id:initial)if(!inventory.containsKey(id))try{ApplicationInfo info=manager.getApplicationInfo(id,0);inventory.put(id,new Entry(manager.getApplicationLabel(info).toString(),id,null));}catch(PackageManager.NameNotFoundException ignored){}
        ArrayList<Entry> all=new ArrayList<>(inventory.values());all.sort((a,b)->a.name.compareToIgnoreCase(b.name));LinkedHashSet<String> chosen=new LinkedHashSet<>(initial);ArrayList<Entry> visible=new ArrayList<>(all);
        boolean dark=activity.getSharedPreferences("appearance",0).getBoolean("dark",true);int foreground=Color.parseColor(dark?"#E4EDF7":"#1C293E"),muted=Color.parseColor(dark?"#A8BACD":"#53667A");float density=activity.getResources().getDisplayMetrics().density;
        LinearLayout panel=new LinearLayout(activity);panel.setOrientation(1);int gap=(int)(16*density);panel.setPadding(gap,0,gap,gap);
        EditText search=new EditText(activity);search.setSingleLine(true);search.setHint("Search apps or packages");search.setTextColor(foreground);search.setTextSize(15);panel.addView(search);
        TextView count=new TextView(activity);count.setTextColor(muted);count.setPadding(0,gap/2,0,gap/2);panel.addView(count);
        ListView list=new ListView(activity);list.setDivider(null);panel.addView(list,new LinearLayout.LayoutParams(-1,Math.min((int)(350*density),(int)(activity.getResources().getDisplayMetrics().heightPixels*0.45))));
        BaseAdapter adapter=new BaseAdapter(){
            public int getCount(){return visible.size();}public Entry getItem(int index){return visible.get(index);}public long getItemId(int index){return index;}
            public View getView(int position,View reused,ViewGroup parent){
                Entry entry=getItem(position);LinearLayout row=new LinearLayout(activity);row.setGravity(Gravity.CENTER_VERTICAL);row.setPadding(gap/2,gap/2,gap/2,gap/2);
                ImageView icon=new ImageView(activity);if(entry.icon==null)try{entry.icon=manager.getApplicationIcon(entry.id);}catch(Exception ignored){}icon.setImageDrawable(entry.icon);row.addView(icon,new LinearLayout.LayoutParams((int)(38*density),(int)(38*density)));
                LinearLayout labels=new LinearLayout(activity);labels.setOrientation(1);labels.setPadding(gap,0,gap/2,0);TextView name=new TextView(activity);name.setText(entry.name);name.setTextSize(15);name.setTextColor(foreground);name.setTypeface(null,Typeface.BOLD);labels.addView(name);TextView details=new TextView(activity);details.setText(entry.id);details.setTextSize(11);details.setTextColor(muted);details.setMaxLines(2);labels.addView(details);row.addView(labels,new LinearLayout.LayoutParams(0,-2,1));
                CompoundButton check=multiple?new CheckBox(activity):new RadioButton(activity);check.setChecked(chosen.contains(entry.id));check.setClickable(false);check.setFocusable(false);row.addView(check);row.setContentDescription(entry.name+", "+entry.id+(chosen.contains(entry.id)?", selected":""));return row;
            }
        };list.setAdapter(adapter);
        Runnable summary=()->count.setText(multiple?chosen.size()+" selected · "+visible.size()+" apps":visible.size()+" apps · select one");summary.run();
        list.setOnItemClickListener((parent,view,index,id)->{String value=visible.get(index).id;if(multiple){if(!chosen.remove(value))chosen.add(value);}else{chosen.clear();chosen.add(value);}adapter.notifyDataSetChanged();summary.run();});
        search.addTextChangedListener(new TextWatcher(){public void beforeTextChanged(CharSequence s,int start,int count,int after){}public void onTextChanged(CharSequence s,int start,int before,int count){String query=s.toString().trim().toLowerCase(Locale.ROOT);visible.clear();for(Entry entry:all)if(entry.name.toLowerCase(Locale.ROOT).contains(query)||entry.id.toLowerCase(Locale.ROOT).contains(query))visible.add(entry);adapter.notifyDataSetChanged();summary.run();}public void afterTextChanged(Editable e){}});
        AlertDialog dialog=new AlertDialog.Builder(activity).setTitle(title).setView(panel).setNegativeButton("Cancel",null).setNeutralButton("Clear selection",null).setPositiveButton(multiple?"Apply selection":"Use app",null).create();
        dialog.setOnShowListener(d->{dialog.getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN);dialog.getButton(-3).setOnClickListener(v->{chosen.clear();adapter.notifyDataSetChanged();summary.run();});dialog.getButton(-1).setOnClickListener(v->{if(!multiple&&chosen.isEmpty())return;result.apply(new LinkedHashSet<>(chosen));dialog.dismiss();});});dialog.show();
    }
}
