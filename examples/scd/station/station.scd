<?xml version='1.0' encoding='UTF-8'?>
<SCL xmlns="http://www.iec.ch/61850/2003/SCL" version="2007" revision="B" release="4">
  <Header id="StationDemo" version="1" revision="0" nameStructure="IEDName"/>
  <Substation name="Demo">
    <VoltageLevel name="E1">
      <Voltage unit="V" multiplier="k">110</Voltage>
      <Bay name="Q01">
        <ConductingEquipment name="Q0" type="CBR">
          <LNode iedName="BayIED" ldInst="CTRL" lnClass="XCBR" lnInst="1"/>
        </ConductingEquipment>
      </Bay>
    </VoltageLevel>
  </Substation>
  <Communication>
    <SubNetwork name="StationBus" type="8-MMS">
      <ConnectedAP iedName="BayIED" apName="S1">
        <Address>
          <P type="IP">127.0.0.1</P>
          <P type="IP-TCP-PORT">8102</P>
        </Address>
        <GSE ldInst="CTRL" cbName="gcbPos">
          <Address>
            <P type="MAC-Address">01-0C-CD-01-00-01</P>
            <P type="APPID">1001</P>
            <P type="VLAN-ID">07B</P>
            <P type="VLAN-PRIORITY">4</P>
          </Address>
          <MinTime unit="s" multiplier="m">10</MinTime>
          <MaxTime unit="s" multiplier="m">500</MaxTime>
        </GSE>
      </ConnectedAP>
      <ConnectedAP iedName="StationHMI" apName="S1"/>
    </SubNetwork>
    <SubNetwork name="ProcessBus" type="8-MMS">
      <ConnectedAP iedName="MUA" apName="AP1">
        <SMV ldInst="LD0" cbName="MSVCB01">
          <Address>
            <P type="MAC-Address">01-0C-CD-04-00-01</P>
            <P type="APPID">4001</P>
            <P type="VLAN-ID">007</P>
            <P type="VLAN-PRIORITY">4</P>
          </Address>
        </SMV>
      </ConnectedAP>
    </SubNetwork>
  </Communication>
  <IED name="BayIED">
    <AccessPoint name="S1">
      <Server>
        <Authentication/>
        <LDevice inst="CTRL">
          <LN0 lnClass="LLN0" inst="" lnType="CommonLLN0">
            <DataSet name="dsPos">
              <FCDA ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" daName="stVal" fc="ST"/>
              <FCDA ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" daName="q" fc="ST"/>
              <FCDA ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" daName="t" fc="ST"/>
            </DataSet>
            <DataSet name="dsEvents">
              <FCDA ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" fc="ST"/>
            </DataSet>
            <ReportControl name="events" datSet="dsPos" confRev="1" buffered="false" indexed="true">
              <TrgOps dchg="true" qchg="true" gi="true"/>
              <OptFields seqNum="true" timeStamp="true" reasonCode="true" dataSet="true" configRef="true"/>
              <RptEnabled max="1"/>
            </ReportControl>
            <ReportControl name="history" datSet="dsEvents" confRev="1" buffered="true" indexed="true">
              <TrgOps dchg="true" qchg="true" gi="true"/>
              <OptFields seqNum="true" timeStamp="true" reasonCode="true" dataSet="true" configRef="true" entryID="true" bufOvfl="true"/>
              <RptEnabled max="1"/>
            </ReportControl>
            <Inputs>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TCTR" lnInst="1" doName="Amp" daName="instMag.i" intAddr="I1" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TVTR" lnInst="1" doName="Vol" daName="instMag.i" intAddr="U1" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TCTR" lnInst="2" doName="Amp" daName="instMag.i" intAddr="I2" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TVTR" lnInst="2" doName="Vol" daName="instMag.i" intAddr="U2" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TCTR" lnInst="3" doName="Amp" daName="instMag.i" intAddr="I3" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
              <ExtRef iedName="MUA" ldInst="LD0" lnClass="TVTR" lnInst="3" doName="Vol" daName="instMag.i" intAddr="U3" serviceType="SMV" srcLDInst="LD0" srcLNClass="LLN0" srcCBName="MSVCB01"/>
            </Inputs>
            <GSEControl name="gcbPos" type="GOOSE" appID="BayPosition" datSet="dsPos" confRev="1">
              <IEDName>StationHMI</IEDName>
            </GSEControl>
          </LN0>
          <LN lnClass="LPHD" inst="1" lnType="Physical"/>
          <LN lnClass="MMXU" inst="1" lnType="Meter"/>
          <LN lnClass="XCBR" inst="1" lnType="Breaker">
            <DOI name="Pos">
              <DAI name="ctlModel">
                <Val>direct-with-normal-security</Val>
              </DAI>
            </DOI>
          </LN>
        </LDevice>
      </Server>
    </AccessPoint>
  </IED>
  <IED name="StationHMI">
    <AccessPoint name="S1">
      <Server>
        <Authentication/>
        <LDevice inst="MON">
          <LN0 lnClass="LLN0" inst="" lnType="CommonLLN0">
            <Inputs>
              <ExtRef iedName="BayIED" ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" daName="stVal" intAddr="breakerPosition" serviceType="GOOSE" srcLDInst="CTRL" srcLNClass="LLN0" srcCBName="gcbPos"/>
            </Inputs>
          </LN0>
          <LN lnClass="LPHD" inst="1" lnType="Physical"/>
        </LDevice>
      </Server>
    </AccessPoint>
  </IED>
  <IED name="MUA">
    <AccessPoint name="AP1">
      <Server>
        <Authentication/>
        <LDevice inst="LD0">
          <LN0 lnClass="LLN0" inst="" lnType="MU_Zero">
            <DataSet name="PhsMeas1">
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
            </DataSet>
            <SampledValueControl name="MSVCB01" smvID="MUA_Samples" datSet="PhsMeas1" confRev="1" smpRate="80" nofASDU="2" smpMod="SmpPerPeriod" multicast="true">
              <SmvOpts refreshTime="false" sampleSynchronized="true" sampleRate="true" dataSet="false" security="false"/>
            </SampledValueControl>
          </LN0>
          <LN lnClass="TCTR" inst="1" lnType="MU_TCTR"/>
          <LN lnClass="TCTR" inst="2" lnType="MU_TCTR"/>
          <LN lnClass="TCTR" inst="3" lnType="MU_TCTR"/>
          <LN lnClass="TCTR" inst="4" lnType="MU_TCTR"/>
          <LN lnClass="TVTR" inst="1" lnType="MU_TVTR"/>
          <LN lnClass="TVTR" inst="2" lnType="MU_TVTR"/>
          <LN lnClass="TVTR" inst="3" lnType="MU_TVTR"/>
          <LN lnClass="TVTR" inst="4" lnType="MU_TVTR"/>
        </LDevice>
      </Server>
    </AccessPoint>
  </IED>
  <DataTypeTemplates>
    <LNodeType id="CommonLLN0" lnClass="LLN0">
      <DO name="Mod" type="Mode"/>
      <DO name="Beh" type="Behaviour"/>
      <DO name="Health" type="Health"/>
      <DO name="NamPlt" type="Nameplate"/>
    </LNodeType>
    <LNodeType id="Physical" lnClass="LPHD">
      <DO name="PhyNam" type="DevicePlate"/>
      <DO name="PhyHealth" type="Health"/>
      <DO name="Proxy" type="Indication"/>
    </LNodeType>
    <LNodeType id="Meter" lnClass="MMXU">
      <DO name="Mod" type="Mode"/>
      <DO name="Beh" type="Behaviour"/>
      <DO name="Health" type="Health"/>
      <DO name="NamPlt" type="Nameplate"/>
      <DO name="TotW" type="Power"/>
    </LNodeType>
    <LNodeType id="Breaker" lnClass="XCBR">
      <DO name="Mod" type="Mode"/>
      <DO name="Beh" type="Behaviour"/>
      <DO name="Health" type="Health"/>
      <DO name="NamPlt" type="Nameplate"/>
      <DO name="Loc" type="Indication"/>
      <DO name="OpCnt" type="Counter"/>
      <DO name="Pos" type="Position"/>
      <DO name="BlkOpn" type="Blocking"/>
      <DO name="BlkCls" type="Blocking"/>
    </LNodeType>
    <LNodeType id="MU_Zero" lnClass="LLN0">
      <DO name="Beh" type="MU_Behaviour"/>
    </LNodeType>
    <LNodeType id="MU_TCTR" lnClass="TCTR">
      <DO name="Amp" type="MU_SAV"/>
    </LNodeType>
    <LNodeType id="MU_TVTR" lnClass="TVTR">
      <DO name="Vol" type="MU_SAV"/>
    </LNodeType>
    <DOType id="Position" cdc="DPC">
      <DA name="stVal" bType="Dbpos" fc="ST" dchg="true"/>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
      <DA name="Oper" bType="Struct" type="Operate" fc="CO"/>
      <DA name="ctlModel" bType="Enum" type="ControlModel" fc="CF"/>
    </DOType>
    <DOType id="Power" cdc="MV" desc="Active power in watts">
      <DA name="mag" bType="Struct" type="Analogue" fc="MX" dchg="true"/>
      <DA name="q" bType="Quality" fc="MX" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="MX"/>
    </DOType>
    <DOType id="Mode" cdc="ENC">
      <DA name="stVal" bType="Enum" type="ModeKind" fc="ST" dchg="true">
        <Val>on</Val>
      </DA>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
      <DA name="ctlModel" bType="Enum" type="ControlModel" fc="CF">
        <Val>status-only</Val>
      </DA>
    </DOType>
    <DOType id="Behaviour" cdc="ENS">
      <DA name="stVal" bType="Enum" type="ModeKind" fc="ST" dchg="true">
        <Val>on</Val>
      </DA>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
    </DOType>
    <DOType id="Health" cdc="ENS">
      <DA name="stVal" bType="Enum" type="HealthKind" fc="ST" dchg="true">
        <Val>Ok</Val>
      </DA>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
    </DOType>
    <DOType id="Indication" cdc="SPS">
      <DA name="stVal" bType="BOOLEAN" fc="ST" dchg="true"/>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
    </DOType>
    <DOType id="Counter" cdc="INS">
      <DA name="stVal" bType="INT32" fc="ST" dchg="true"/>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
    </DOType>
    <DOType id="Blocking" cdc="SPC">
      <DA name="stVal" bType="BOOLEAN" fc="ST" dchg="true"/>
      <DA name="q" bType="Quality" fc="ST" qchg="true"/>
      <DA name="t" bType="Timestamp" fc="ST"/>
      <DA name="ctlModel" bType="Enum" type="ControlModel" fc="CF">
        <Val>status-only</Val>
      </DA>
    </DOType>
    <DOType id="Nameplate" cdc="LPL">
      <DA name="vendor" bType="VisString255" fc="DC">
        <Val>Example</Val>
      </DA>
      <DA name="swRev" bType="VisString255" fc="DC">
        <Val>1</Val>
      </DA>
      <DA name="d" bType="VisString255" fc="DC"/>
      <DA name="configRev" bType="VisString255" fc="DC">
        <Val>1</Val>
      </DA>
    </DOType>
    <DOType id="DevicePlate" cdc="DPL">
      <DA name="vendor" bType="VisString255" fc="DC">
        <Val>Example</Val>
      </DA>
    </DOType>
    <DOType id="MU_Behaviour" cdc="ENS">
      <DA name="stVal" bType="Enum" type="MU_BehaviourMode" fc="ST"/>
    </DOType>
    <DOType id="MU_SAV" cdc="SAV">
      <DA name="instMag" bType="Struct" type="MU_Analog" fc="MX"/>
      <DA name="q" bType="Quality" fc="MX"/>
    </DOType>
    <DAType id="Analogue">
      <BDA name="f" bType="FLOAT32"/>
    </DAType>
    <DAType id="Operate">
      <BDA name="ctlVal" bType="BOOLEAN"/>
      <BDA name="origin" bType="Struct" type="Origin"/>
      <BDA name="ctlNum" bType="INT8U"/>
      <BDA name="T" bType="Timestamp"/>
      <BDA name="Test" bType="BOOLEAN"/>
      <BDA name="Check" bType="Check"/>
    </DAType>
    <DAType id="Origin">
      <BDA name="orCat" bType="Enum" type="OriginCategory"/>
      <BDA name="orIdent" bType="Octet64"/>
    </DAType>
    <DAType id="MU_Analog">
      <BDA name="i" bType="INT32"/>
    </DAType>
    <EnumType id="ControlModel">
      <EnumVal ord="0">status-only</EnumVal>
      <EnumVal ord="1">direct-with-normal-security</EnumVal>
      <EnumVal ord="2">sbo-with-normal-security</EnumVal>
      <EnumVal ord="3">direct-with-enhanced-security</EnumVal>
      <EnumVal ord="4">sbo-with-enhanced-security</EnumVal>
    </EnumType>
    <EnumType id="OriginCategory">
      <EnumVal ord="0">not-supported</EnumVal>
      <EnumVal ord="1">bay-control</EnumVal>
      <EnumVal ord="2">station-control</EnumVal>
      <EnumVal ord="3">remote-control</EnumVal>
      <EnumVal ord="4">automatic-bay</EnumVal>
      <EnumVal ord="5">automatic-station</EnumVal>
      <EnumVal ord="6">automatic-remote</EnumVal>
      <EnumVal ord="7">maintenance</EnumVal>
      <EnumVal ord="8">process</EnumVal>
    </EnumType>
    <EnumType id="ModeKind">
      <EnumVal ord="1">on</EnumVal>
      <EnumVal ord="2">blocked</EnumVal>
      <EnumVal ord="3">test</EnumVal>
      <EnumVal ord="4">test/blocked</EnumVal>
      <EnumVal ord="5">off</EnumVal>
    </EnumType>
    <EnumType id="HealthKind">
      <EnumVal ord="1">Ok</EnumVal>
      <EnumVal ord="2">Warning</EnumVal>
      <EnumVal ord="3">Alarm</EnumVal>
    </EnumType>
    <EnumType id="MU_BehaviourMode">
      <EnumVal ord="1">on</EnumVal>
      <EnumVal ord="2">blocked</EnumVal>
      <EnumVal ord="3">test</EnumVal>
      <EnumVal ord="4">test/blocked</EnumVal>
      <EnumVal ord="5">off</EnumVal>
    </EnumType>
  </DataTypeTemplates>
</SCL>
