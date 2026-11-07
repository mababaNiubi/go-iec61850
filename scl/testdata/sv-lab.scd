<?xml version="1.0" encoding="UTF-8"?>
<SCL xmlns="http://www.iec.ch/61850/2003/SCL" version="2007" revision="B" release="4">
 <Header id="SV-Lab"/>
 <Communication><SubNetwork name="Process" type="8-MMS">
  <ConnectedAP iedName="MUA" apName="AP1"><SMV ldInst="LD0" cbName="MSVCB01"><Address>
   <P type="MAC-Address">01-0C-CD-04-00-01</P><P type="APPID">4001</P><P type="VLAN-ID">007</P><P type="VLAN-PRIORITY">4</P>
  </Address></SMV></ConnectedAP>
  <ConnectedAP iedName="MUB" apName="AP1"><SMV ldInst="LD0" cbName="MSVCB01"><Address>
   <P type="MAC-Address">01-0C-CD-04-00-02</P><P type="APPID">4002</P><P type="VLAN-ID">007</P><P type="VLAN-PRIORITY">4</P>
  </Address></SMV></ConnectedAP>
 </SubNetwork></Communication>
 <IED name="MUA"><AccessPoint name="AP1"><Server><Authentication/><LDevice inst="LD0">
  <LN0 lnClass="LLN0" inst="" lnType="Zero"><DataSet name="PhsMeas1">
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="1" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="1" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="2" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="2" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="3" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="3" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="4" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="4" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="1" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="1" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="2" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="2" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="3" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="3" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="4" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="4" doName="Vol" daName="q" fc="MX"/>
  </DataSet><SampledValueControl name="MSVCB01" smvID="MUA_Samples" datSet="PhsMeas1" confRev="1" smpRate="80" nofASDU="2" smpMod="SmpPerPeriod" multicast="true">
   <SmvOpts refreshTime="false" sampleSynchronized="true" sampleRate="true" dataSet="false" security="false"/>
  </SampledValueControl></LN0>
  <LN lnClass="TCTR" inst="1" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="2" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="3" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="4" lnType="TCTR"/>
  <LN lnClass="TVTR" inst="1" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="2" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="3" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="4" lnType="TVTR"/>
 </LDevice></Server></AccessPoint></IED>
 <IED name="MUB"><AccessPoint name="AP1"><Server><Authentication/><LDevice inst="LD0">
  <LN0 lnClass="LLN0" inst="" lnType="Zero"><DataSet name="PhsMeas1">
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="1" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="1" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="2" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="2" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="3" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="3" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="4" doName="Amp" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TCTR" lnInst="4" doName="Amp" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="1" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="1" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="2" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="2" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="3" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="3" doName="Vol" daName="q" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="4" doName="Vol" daName="instMag" fc="MX"/>
   <FCDA ldInst="LD0" lnClass="TVTR" lnInst="4" doName="Vol" daName="q" fc="MX"/>
  </DataSet><SampledValueControl name="MSVCB01" smvID="MUB_Samples" datSet="PhsMeas1" confRev="1" smpRate="80" nofASDU="2" smpMod="SmpPerPeriod" multicast="true">
   <SmvOpts refreshTime="false" sampleSynchronized="true" sampleRate="true" dataSet="false" security="false"/>
  </SampledValueControl></LN0>
  <LN lnClass="TCTR" inst="1" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="2" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="3" lnType="TCTR"/>
  <LN lnClass="TCTR" inst="4" lnType="TCTR"/>
  <LN lnClass="TVTR" inst="1" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="2" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="3" lnType="TVTR"/>
  <LN lnClass="TVTR" inst="4" lnType="TVTR"/>
 </LDevice></Server></AccessPoint></IED>
 <DataTypeTemplates>
  <LNodeType id="Zero" lnClass="LLN0"><DO name="Beh" type="Behaviour"/></LNodeType>
  <LNodeType id="TCTR" lnClass="TCTR"><DO name="Amp" type="SAV"/></LNodeType>
  <LNodeType id="TVTR" lnClass="TVTR"><DO name="Vol" type="SAV"/></LNodeType>
  <DOType id="Behaviour" cdc="ENS"><DA name="stVal" bType="Enum" type="BehaviourMode" fc="ST"/></DOType>
  <DOType id="SAV" cdc="SAV"><DA name="instMag" bType="Struct" type="Analog" fc="MX"/><DA name="q" bType="Quality" fc="MX"/></DOType>
  <DAType id="Analog"><BDA name="i" bType="INT32"/></DAType>
  <EnumType id="BehaviourMode"><EnumVal ord="1">on</EnumVal><EnumVal ord="2">blocked</EnumVal><EnumVal ord="3">test</EnumVal><EnumVal ord="4">test/blocked</EnumVal><EnumVal ord="5">off</EnumVal></EnumType>
 </DataTypeTemplates>
</SCL>
