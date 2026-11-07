<?xml version="1.0" encoding="UTF-8"?>
<SCL xmlns="http://www.iec.ch/61850/2003/SCL" version="2007" revision="B" release="4">
 <Header id="SV-Types-Lab"/>
 <Communication><SubNetwork name="Lab" type="8-MMS"><ConnectedAP iedName="TypedMU" apName="AP1">
  <SMV ldInst="LD0" cbName="SV1"><Address>
   <P type="MAC-Address">01-0C-CD-04-00-03</P><P type="APPID">4003</P><P type="VLAN-ID">000</P><P type="VLAN-PRIORITY">4</P>
  </Address></SMV>
 </ConnectedAP></SubNetwork></Communication>
 <IED name="TypedMU"><AccessPoint name="AP1"><Server><Authentication/><LDevice inst="LD0">
  <LN0 lnClass="LLN0" inst="" lnType="Zero"><DataSet name="Mixed">
   <FCDA ldInst="LD0" lnClass="GGIO" lnInst="1" doName="AnIn1" fc="MX"/>
  </DataSet><SampledValueControl name="SV1" smvID="TypedSamples" datSet="Mixed" confRev="1" smpRate="4800" nofASDU="1" smpMod="SmpPerSec" multicast="true">
   <SmvOpts refreshTime="false" sampleSynchronized="true" sampleRate="true" dataSet="false" security="false"/>
  </SampledValueControl></LN0>
  <LN lnClass="GGIO" inst="1" lnType="IO"/>
 </LDevice></Server></AccessPoint></IED>
 <DataTypeTemplates>
  <LNodeType id="Zero" lnClass="LLN0"><DO name="Beh" type="Behaviour"/></LNodeType>
  <LNodeType id="IO" lnClass="GGIO"><DO name="AnIn1" type="Sample"/></LNodeType>
  <DOType id="Behaviour" cdc="ENS"><DA name="stVal" bType="Enum" type="BehaviourMode" fc="ST"/></DOType>
  <DOType id="Sample" cdc="MV">
   <DA name="mode" bType="Enum" type="Mode" fc="MX"/>
   <DA name="q" bType="Quality" fc="MX"/>
   <DA name="values" bType="FLOAT32" count="2" fc="MX"/>
   <DA name="large" bType="INT64" fc="MX"/>
  </DOType>
  <EnumType id="Mode"><EnumVal ord="-2">negative-test</EnumVal></EnumType>
  <EnumType id="BehaviourMode"><EnumVal ord="1">on</EnumVal><EnumVal ord="2">blocked</EnumVal><EnumVal ord="3">test</EnumVal><EnumVal ord="4">test/blocked</EnumVal><EnumVal ord="5">off</EnumVal></EnumType>
 </DataTypeTemplates>
</SCL>
